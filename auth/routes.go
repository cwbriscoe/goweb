package auth

import (
	"encoding/base64"
	"fmt"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cwbriscoe/goutil/str"
	"github.com/cwbriscoe/goweb/limiter"
	"github.com/goccy/go-json"
	"github.com/jackc/pgx/v5"
)

// addRoutes adds auth routhes
func (a *Auth) addRoutes() {
	if a.config.EnableRegistration {
		a.config.Router.HandlerFunc("POST", "/auth/register/", a.registerHandler())
	}
	a.config.Router.HandlerFunc("POST", "/auth/signin/", a.signInHandler())
	a.config.Router.HandlerFunc("GET", "/auth/signout/", a.signOutHandler())
	a.config.Router.HandlerFunc("GET", "/auth/test/", a.testHandler())
}

// handlePanic will recover and log a panic.
func (a *Auth) handlePanic(f http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if i := recover(); i != nil {
				a.log.Error().Msgf("panic(recovered) at %s: %v", r.URL.Path, i)
				w.WriteHeader(http.StatusInternalServerError)
			}
		}()
		f(w, r)
	}
}

// authLimiter limits the rate that single user or the sum of users can access auth requests
func (a *Auth) authLimiter(f http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := a.limiter.LimitRequest(w, r); err != nil {
			limiter.WriteErrorResponse(w, err)
			return
		}
		f(w, r)
	}
}

// create the register handler
func (a *Auth) registerHandler() http.HandlerFunc {
	return a.handlePanic(a.authLimiter(a.register()))
}

type register struct {
	Email string `json:"email"`
	User  string `json:"user"`
	Pass  string `json:"pass"`
}

func (a *Auth) register() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var reg register
		err := json.NewDecoder(r.Body).Decode(&reg)
		if err != nil {
			a.log.Err(err).Msg("register: error decoding request body")
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		resp := a.validateRegistration(&reg)
		if resp != nil {
			if _, err = w.Write(resp); err != nil {
				a.log.Err(err).Msg("register: error writing response to body")
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			return
		}

		err = a.registerUser(&reg)
		if err != nil {
			a.log.Err(err).Msg("register: error inserting user into db")
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		a.log.Info().Msgf("%s successfully registered", reg.User)
	}
}

// create the signin handler
func (a *Auth) signInHandler() http.HandlerFunc {
	return a.handlePanic(a.authLimiter(a.signIn()))
}

//revive:disable cognitive-complexity

func (a *Auth) signIn() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// make sure we are signed out first
		name := a.signOutInternal(w, r)
		if name != "UNKNOWN" {
			a.log.Info().Msgf("%s successful signout", name)
		}

		user := &signin{}
		// get the JSON body and decode into credentials
		err := json.NewDecoder(r.Body).Decode(&user)
		if err != nil {
			// if the structure of the body is wrong, return an HTTP error.
			a.log.Err(err).Msg("signin: error decoding request body")
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		// check that user and pass audit checks before hitting the db.
		if checkUsername(user.User) != nil || checkPassword(user.Pass) != nil {
			if len(user.User) > maxUsernameLen {
				user.User = user.User[:maxUsernameLen]
			}
			userName := str.ToASCII(user.User)
			a.log.Warn().Msgf("%s tried to signin with a malformed username or password", userName)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		// get password hash from db
		var hash string
		hash, err = a.getSecurityInfo(user)
		if err == pgx.ErrNoRows {
			a.log.Warn().Msgf("%s tried to signin with an invalid username", user.User)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if err != nil {
			a.log.Err(err).Msg("signin: error getting hash from db")
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		// now compare the hash with the password
		var valid bool
		valid, err = a.compare(hash, user.Pass)
		if err != nil {
			a.log.Err(err).Msg("signin: comparing password")
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if !valid {
			a.log.Warn().Msgf("%s tried to signin with an invalid password", user.User)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		// authentication passed, create the auth tokens
		user.expires = time.Now().Add(a.config.RefreshExpire)
		user.session = int(rand.Int31())
		if err = a.createTokens(w, user); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		a.log.Info().Msgf("%s successful signin", strconv.Itoa(user.id)+"|"+user.User)

		go func() {
			if err := a.createSession(user); err != nil {
				a.log.Err(err).Msg("signin: error creating new session")
			}
		}()
	}
}

// create the signout handler
func (a *Auth) signOutHandler() http.HandlerFunc {
	return a.handlePanic(a.authLimiter(a.signOut()))
}

func (a *Auth) signOut() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := a.signOutInternal(w, r)
		a.log.Info().Msgf("%s successful signout", user)
		http.Redirect(w, r, "/", http.StatusSeeOther)
	}
}

func (a *Auth) signOutInternal(w http.ResponseWriter, r *http.Request) string {
	user := "UNKNOWN"
	claims, success := a.getClaims(r, "refresh")
	if success {
		user = claims.Subject
		go func() {
			creds := strings.Split(claims.Subject, "|")
			if len(creds) != 2 {
				a.log.Warn().Msgf("signout: claims.Subject had a length != 2")
				return
			}

			id, err := strconv.Atoi(creds[0])
			if err != nil {
				a.log.Warn().Msgf("signout: atoi failed to convert string id to int")
				return
			}

			sess, err := strconv.Atoi(claims.ID)
			if err != nil {
				a.log.Warn().Msgf("signout: atoi failed to convert string sess to int")
				return
			}

			if err := a.deleteSession(id, sess); err != nil {
				a.log.Err(err).Msg("signout: error deleting session")
			}
		}()
	}

	a.deleteCookie(w, "id")
	a.deleteCookie(w, "session")
	a.deleteCookie(w, "access")
	a.deleteCookie(w, "refresh")
	return user
}

// create test handler
func (a *Auth) testHandler() http.HandlerFunc {
	return a.handlePanic(a.authLimiter(a.AuthHandler("admin", a.test())))
}

func (a *Auth) test() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims, success := a.getClaims(r, "access")
		dataAccess := []byte("refresh to see")
		var err error
		if success {
			dataAccess, err = json.Marshal(claims)
			if err != nil {
				a.log.Err(err).Msg("test: error marshalling claims")
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
		}

		claims, success = a.getClaims(r, "session")
		if !success {
			a.log.Debug().Msg("test called without a valid user token")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		dataUser, err := json.Marshal(claims)
		if err != nil {
			a.log.Err(err).Msg("test: error marshalling claims")
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		claims, success = a.getClaims(r, "refresh")
		if !success {
			a.log.Debug().Msg("test called without a valid refresh token")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		dataRefresh, err := json.Marshal(claims)
		if err != nil {
			a.log.Err(err).Msg("test: error marshalling claims")
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		var dataID []byte
		c, err := r.Cookie("id")
		if err == nil {
			dataID, err = base64.URLEncoding.DecodeString(c.Value)
			if err != nil {
				a.log.Err(err).Msg("test: error decoding id")
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
		}

		_, _ = w.Write([]byte(fmt.Sprintf("Welcome %s!\n", claims.Subject)))

		_, _ = w.Write([]byte("\naccess:  "))
		_, _ = w.Write(dataAccess)

		_, _ = w.Write([]byte("\nrefresh: "))
		_, _ = w.Write(dataRefresh)

		_, _ = w.Write([]byte("\nsession: "))
		_, _ = w.Write(dataUser)

		_, _ = w.Write([]byte("\nid:      "))
		_, _ = w.Write(dataID)

		a.log.Debug().Msgf("%s test successfully authenticated", claims.Subject)
	}
}

//revive:enable cognitive-complexity
