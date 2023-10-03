// Copyright 2023 Christopher Briscoe.  All rights reserved.

// Package job is used to run a daemon to run batch jobs on a schedule
package job

import (
	"bufio"
	"context"
	"os/exec"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/cwbriscoe/goutil/db"
	"github.com/cwbriscoe/goutil/logging"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//revive:disable:max-public-structs

// RunCallback will be called to run the submitted process.
type RunCallback func(*Entry) error

// Manager is an instance of a job manager.
type Manager struct {
	app            string
	env            string
	url            string
	db             *pgxpool.Pool
	log            *logging.Logger
	rootDir        string
	logDir         string
	interval       time.Duration
	maxConcurrency int
	callback       RunCallback
}

// ManagerOptions contain the settings to use when creating a new job
// manager instance.
type ManagerOptions struct {
	App            string
	Env            string
	URL            string
	DB             *pgxpool.Pool
	RootDir        string
	LogDir         string
	ScanInterval   time.Duration
	MaxConcurrency int
	RunCallback    RunCallback
}

// Entry stores resources and information about running
// jobs.  Can be used by running jobs to call utility methods.
type Entry struct {
	App     string
	Env     string
	URL     string
	RootDir string
	JobID   int
	RunID   int
	Name    string
	NameKey string
	Fun     string
	DB      *pgxpool.Pool
	Log     *logging.Logger
	Ctx     context.Context
}

// LogDivider can be used to divide logical sections in the log output.
var LogDivider = strings.Repeat("=", 80)

// NewManager initializes a new job manager and returns a pointer.
func NewManager(options *ManagerOptions) (*Manager, error) {
	var err error
	manager := &Manager{
		app:            options.App,
		env:            options.Env,
		url:            options.URL,
		db:             options.DB,
		interval:       options.ScanInterval,
		maxConcurrency: options.MaxConcurrency,
		callback:       options.RunCallback,
		rootDir:        options.RootDir,
		logDir:         options.LogDir,
	}

	manager.log, err = logging.NewLogger(logging.Config{
		BaseDir:    manager.logDir,
		FileName:   "jobmanager.log",
		MaxAge:     time.Hour * 24 * 28,
		MaxSize:    1 * 1000 * 1000,
		MaxBackups: 28,
		Console:    false,
		Compress:   true,
	})
	if err != nil {
		return nil, err
	}

	return manager, nil
}

// Run starts the job submitting and monitoring process.
func (m *Manager) Run() {
	m.log.Info().Msg("job manager started")

	// first mark any active jobs that were running before as cancelled since they didn't finish.
	if err := m.markAbandoned(); err != nil {
		m.log.Err(err).Msg("failed in call to markAbandoned()")
	}

	for {
		// m.log.Info().Msg("starting scan for jobs to submit")
		m.submit()
		// m.log.Info().Msgf("ending scan, sleeping for %s", m.interval.String())
		time.Sleep(m.interval)
	}
}

//revive:disable:cyclomatic
//revive:disable:cognitive-complexity
func (m *Manager) submit() {
	for {
		entry, err := m.getJob()
		if err != nil {
			m.log.Err(err).Msg("error calling getJob()")
			return
		}
		if entry == nil {
			return
		}

		entry.Name = strings.TrimSpace(entry.Name)
		entry.NameKey = strings.ReplaceAll(strings.ToLower(entry.Name), " ", "_")
		logFile := entry.NameKey + ".log"

		entry.Log, err = logging.NewLogger(logging.Config{
			BaseDir:    path.Join(m.logDir, "job"),
			FileName:   logFile,
			MaxAge:     time.Hour * 24 * 30,
			MaxSize:    10 * 1000 * 1000,
			MaxBackups: 100,
			Console:    false,
			Compress:   true,
		})
		if err != nil {
			m.log.Err(err).Msgf("error running new logger for file: %s", path.Join(path.Join(m.logDir, "job"), logFile))
			return
		}

		entry.RunID, err = m.markStarted(entry)
		if err != nil {
			m.log.Err(err).Msg("error calling markStarted()")
			return
		}

		entry.DB = m.db
		entry.Ctx = context.Background()

		go func() {
			defer func() {
				if i := recover(); i != nil {
					m.log.Warn().Msgf("recovered from panic in submitted job %d", entry.RunID)
					m.log.Warn().Msgf("panic info: %v", i)

					err = m.markEnded(entry.RunID, entry.JobID, "panic")
					if err != nil {
						m.log.Err(err).Msg("error calling markended(panic)")
					}
				}
			}()

			start := time.Now()
			m.log.Info().Msgf("job %d started - id: %d, name:'%s', function: '%s'", entry.RunID, entry.JobID, entry.Name, entry.Fun)
			entry.Log.Info().Msg("")
			entry.Log.Info().Msg(LogDivider)
			entry.Log.Info().Msgf("========== job %d %s() starting - %s", entry.RunID, entry.Fun, time.Now().Format("2006-01-02 15:04:05"))
			entry.Log.Info().Msg(LogDivider)

			err = m.callback(entry)
			if err != nil {
				m.log.Err(err).Msgf("job %d error", entry.RunID)
				err2 := m.markEnded(entry.RunID, entry.JobID, "error")
				if err2 != nil {
					m.log.Err(err).Msg("error calling markended(error)")
					return
				}
			}

			end := time.Now()
			duration := end.Sub(start).String()

			entry.Log.Info().Msgf("========== job %d %s() ending - runtime: %s", entry.RunID, entry.Fun, duration)
			entry.Log.Info().Msg(LogDivider)
			m.log.Info().Msgf("job %d ended - runtime: %s", entry.RunID, duration)

			if err == nil {
				err2 := m.markEnded(entry.RunID, entry.JobID, "ok")
				if err2 != nil {
					m.log.Err(err).Msg("error calling markended(ok)")
					return
				}
			}
		}()
	}
}

//revive:enable:cyclomatic
//revive:enable:cognitive-complexity

func (m *Manager) getJob() (*Entry, error) {
	ctx := context.Background()

	var jobid, runid int
	sql := `
select active.job_id
      ,active.run_id
  from job.active
	join job.entry on active.job_id = entry.job_id
 where entry.exclusive = true;`

	err := m.db.QueryRow(ctx, sql).Scan(&jobid, &runid)
	if err != nil && err != pgx.ErrNoRows {
		return nil, err
	}
	// if we get a row, we cannot process a new job since and exclusive job is running now
	if err == nil {
		m.log.Info().Msgf("cannot submit new jobs because exclusive job %d is running", runid)
		return nil, nil
	}

	sql = `
select job_id
      ,name 
      ,function
  from job.entry
 where entry.enabled = true
   and now() > entry.last_run_ts + entry.every
   and not exists(
       select 1
         from job.active
        where active.job_id = entry.job_id
          and entry.multiple = false)
 order by priority, last_run_ts
 limit 1;`

	jobEntry := &Entry{
		App:     m.app,
		Env:     m.env,
		URL:     m.url,
		RootDir: m.rootDir,
	}
	err = m.db.QueryRow(ctx, sql).Scan(&jobEntry.JobID, &jobEntry.Name, &jobEntry.Fun)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}

	var cnt int
	sql = "select count(*) from job.active;"
	err = m.db.QueryRow(ctx, sql).Scan(&cnt)
	if err != nil && err != pgx.ErrNoRows {
		return nil, err
	}
	if cnt >= m.maxConcurrency {
		m.log.Info().Msgf("cannot submit job %d because max concurrency of %d has been reached", jobEntry.JobID, cnt)
		return nil, nil
	}

	return jobEntry, nil
}

func (m *Manager) markStarted(jobEntry *Entry) (int, error) {
	ctx := context.Background()
	var runid int

	sqlu := "update job.entry set last_run_ts = now() where job_id = $1;"
	_, err := m.db.Exec(ctx, sqlu, jobEntry.JobID)
	if err != nil {
		return -1, err
	}

	sqld := "insert into job.active (job_id, start_ts) values ($1, now()) returning run_id"
	err = m.db.QueryRow(ctx, sqld, jobEntry.JobID).Scan(&runid)
	if err != nil {
		return -1, err
	}

	return runid, nil
}

func (m *Manager) markEnded(runid, jobid int, reason string) error {
	batch := db.NewBatch(context.TODO(), m.db)

	sqli := `
insert into job.completed (run_id, job_id, start_ts, finish_ts, status)
select run_id, job_id, start_ts, now(), $2 from job.active where run_id = $1;`

	sqld := "delete from job.active where run_id = $1;"

	sqlu := "update job.entry set last_run_ts = now() where job_id = $1;"

	batch.Queue(sqli, runid, reason)
	batch.Queue(sqld, runid)
	if reason != "abandoned" {
		batch.Queue(sqlu, jobid)
	}

	_, err := batch.Exec()
	if err != nil {
		return err
	}

	return nil
}

func (m *Manager) markAbandoned() error {
	sql := "select run_id, job_id from job.active;"

	rows, err := m.db.Query(context.TODO(), sql)
	if err != nil {
		return err
	}

	defer rows.Close()
	for rows.Next() {
		var runid, jobid int
		err = rows.Scan(&runid, &jobid)
		if err != nil {
			return err
		}
		if err = m.markEnded(runid, jobid, "abandoned"); err != nil {
			return err
		}
	}

	return rows.Err()
}

/*
*******************************************************************************
Job utility functions that can be called from running jobs (goroutines)
*******************************************************************************
*/

// LogMultiLineString prints out a multiline string and
// prints a line number for each line
func (j *Entry) LogMultiLineString(s string) {
	idx := 1
	scanner := bufio.NewScanner(strings.NewReader(s))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			j.Log.Info().Msgf("%03d %s", idx, scanner.Text())
			idx++
		}
	}
}

// Exec runs an SQL statement that does not need results back.  The function
// Will print the query and then log rows affected and runtime when finished.
func (j *Entry) Exec(ctx context.Context, sql string, args ...any) error {
	j.LogMultiLineString(sql)

	start := time.Now()
	tag, err := j.DB.Exec(ctx, sql, args...)
	end := time.Now()

	if err != nil {
		j.Log.Err(err).Msg("failed to execute sql")
		return err
	}

	j.Log.Info().Msgf("sql executed successfully: time: %s, rows: %d", end.Sub(start).String(), tag.RowsAffected())
	j.Log.Info().Msg(LogDivider)

	return nil
}

// RunCmd will execute the given command and log its output
func (j *Entry) RunCmd(ctx context.Context, cmdstr string) error {
	j.Log.Info().Msgf("cmd: %s", cmdstr)

	args := strings.Fields(cmdstr)
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		j.Log.Err(err).Msg("failed to open stdout pipe")
		return err
	}

	var wg sync.WaitGroup
	wg.Add(1)

	scanner := bufio.NewScanner(stdout)
	go func() {
		for scanner.Scan() {
			j.Log.Info().Msgf("out: %s", scanner.Text())
		}
		wg.Done()
	}()

	start := time.Now()

	if err = cmd.Start(); err != nil {
		j.Log.Err(err).Msg("failed to start command")
		return err
	}

	wg.Wait()

	if err = cmd.Wait(); err != nil {
		j.Log.Err(err).Msg("failed waiting for command to finish")
		return err
	}

	end := time.Now()

	j.Log.Info().Msgf("cmd: executed successfully: time: %s", end.Sub(start).String())
	j.Log.Info().Msg(LogDivider)

	return nil
}
