// Copyright 2023 Christopher Briscoe.  All rights reserved.

package auth

import (
	"context"
	"net/mail"
	"strings"

	"github.com/cwbriscoe/goutil/db"
)

func (*Auth) formatEmail(email string) (string, error) {
	e, err := mail.ParseAddress(email)
	if err != nil {
		return "", err
	}
	return e.Address, nil
}

func (a *Auth) getSecurityInfo(user *signin) (string, error) {
	var id int
	var hash string
	var roles []string

	sql := "select id, hash, roles from usr.auth where name = $1;"
	err := a.config.DB.QueryRow(context.TODO(), sql, user.User).Scan(&id, &hash, &roles)
	if err != nil {
		return "", err
	}

	user.id = id
	user.permissions = roles
	return hash, nil
}

func (a *Auth) revalidateSecurityInfo(user *signin) error {
	var roles []string

	sql := `
	select roles 
	  from usr.auth 
		join usr.sess on sess.auth_id = auth.id
	 where auth.id = $1
	   and auth.name = $2
		 and sess.id = $3;
	`
	err := a.config.DB.QueryRow(context.TODO(), sql, user.id, user.User, user.session).Scan(&roles)
	if err != nil {
		return err
	}

	user.permissions = roles
	return nil
}

func (a *Auth) updateSessionTimestamp(user *signin) error {
	sql := `update usr.sess set last_used_ts = now() where sess.id = $1;`
	_, err := a.config.DB.Exec(context.TODO(), sql, user.session)
	return err
}

func (a *Auth) createSession(user *signin) error {
	sqli := "insert into usr.sess values ($1, $2, now(), $3, now());"
	sqlu := "update usr.auth set last_login_ts = now() where id = $1;"

	batch := db.NewBatch(context.TODO(), a.config.DB)
	batch.Queue(sqli, user.session, user.id, user.expires)
	batch.Queue(sqlu, user.id)

	_, err := batch.Exec()
	if err != nil {
		return err
	}

	return nil
}

func (a *Auth) deleteSession(id, sess int) error {
	sql := "delete from usr.sess where id = $1 and auth_id = $2;"
	_, err := a.config.DB.Exec(context.TODO(), sql, sess, id)
	return err
}

func (a *Auth) registerUser(reg *register) error {
	hash, err := a.generate(reg.Pass)
	if err != nil {
		return err
	}

	lname := strings.ToLower(reg.User)
	lemail, err := a.formatEmail(reg.Email)
	if err != nil {
		return err
	}

	sql := `
insert into usr.auth
(name, lname, email, hash, roles, last_login_ts, create_ts)
values ($1, $2, $3, $4, array['user'], now(), now());
`
	_, err = a.config.DB.Exec(context.TODO(), sql, &reg.User, &lname, &lemail, &hash)
	return err
}

func (a *Auth) checkAlreadyExists(reg *register) (userExists bool, emailExists bool, err error) {
	lname := strings.ToLower(reg.User)
	lemail, err := a.formatEmail(reg.Email)
	if err != nil {
		return false, false, err
	}

	sql := `
select coalesce((select true from usr.auth where lname = $1), false) as user
,coalesce((select true from usr.auth where email = $2), false) as email;
`
	err = a.config.DB.QueryRow(context.TODO(), sql, lname, lemail).Scan(&userExists, &emailExists)
	return userExists, emailExists, err
}

func (a *Auth) purgeExpiredSessions() error {
	sql := `delete from usr.sess where expire_ts < now();`
	_, err := a.config.DB.Exec(context.TODO(), sql)
	return err
}
