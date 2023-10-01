package auth

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/cwbriscoe/goutil/logging"
	"github.com/cwbriscoe/goweb/limiter"
	"github.com/cwbriscoe/goweb/tracker"
	"github.com/goccy/go-json"
	"github.com/golang-jwt/jwt/v4"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/julienschmidt/httprouter"
	"golang.org/x/exp/slices"
)

// Config stores the settings used for all auth requests
type Config struct {
	Issuer             string             // what authority will be issuing the jwt tokens
	SecretPath         string             // path to the file with the secrets
	Router             *httprouter.Router // router used to add auth http endpoints
	AccessExpire       time.Duration      // how long before the access tokens will expire
	RefreshExpire      time.Duration      // how long before the refresh tokens will expire
	UserRate           time.Duration      // max rate that a user can make any auth request
	GlobalRate         time.Duration      // max rate that all users can make any auth request
	LimiterLogger      *logging.Logger    // the rate limiter logger
	DB                 *pgxpool.Pool      // database connection to retrieve stored auth data
	Log                *logging.Logger    // logger for logging auth state changes
	EnableRegistration bool               // feature flag to enable or disable new registration
}

// Auth contains the config
type Auth struct {
	config  *Config          // copy of the config settings
	secret  []byte           // secret used for signing the jwt
	key     []byte           // secret used to encrypt hashed passwords
	pepper  string           // secret used for adding pepper to passwords before hashing
	log     *logging.Logger  // logger for logging auth state changes
	limiter *limiter.Limiter // the request limiter to help mitigate ddos
}

type claims struct {
	jwt.RegisteredClaims
	Permissions []string `json:"scope"`
}

type signin struct {
	User        string    `json:"user"` // read from client
	Pass        string    `json:"pass"` // read from client
	id          int       // the users internal id
	permissions []string  // the access of the user
	session     int       // the users internal session id
	expires     time.Time // the time the refresh token expires
}

// NewAuth creates, configures and returns a new Auth object
func NewAuth(config *Config) *Auth {
	a := &Auth{
		config: config,
		log:    config.Log,
	}

	// load the secrets
	a.loadSecrets(a.config.SecretPath)

	// init api limiter
	var err error
	a.limiter, err = limiter.NewLimiter(
		&limiter.LimitSettings{
			Name: "auth",
			Log:  a.config.LimiterLogger,
			UserRate: limiter.Rate{
				Interval:   a.config.UserRate,
				Burst:      4,
				MaxDelayed: 2,
			},
			GlobalRate: limiter.Rate{
				Interval: a.config.GlobalRate,
				Burst:    4,
			},
		})
	if err != nil {
		panic(err)
	}

	a.addRoutes()

	// kick off go routine to purge expires sessions
	go func() {
		for {
			time.Sleep(time.Hour)
			if err := a.purgeExpiredSessions(); err != nil {
				a.log.Err(err).Msg("goroutine: error purging expired sessions")
			}
		}
	}()

	return a
}

func (a *Auth) loadSecrets(path string) {
	type secrets struct {
		JWTKey string `json:"jwtkey"`
		EncKey string `json:"enckey"`
		Pepper string `json:"pepper"`
	}

	data, err := os.ReadFile(path)
	if err != nil {
		panic(err)
	}

	secret := &secrets{}
	err = json.Unmarshal(data, secret)
	if err != nil {
		panic(err)
	}

	a.secret = []byte(secret.JWTKey)
	a.key = []byte(secret.EncKey)
	a.pepper = secret.Pepper
}

// AuthHandler wraps functions that need authentication before executing.  If
// authentication fails, we return status 401 NotAuthorized.
func (a *Auth) AuthHandler(access string, f http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims, success := a.getClaims(r, "access")
		if !success {
			// no access token found, we need to revalidate permissions using the refresh token if it exists
			claims, success = a.revalidate(w, r)
			if !success {
				http.Redirect(w, r, "/signin/", http.StatusSeeOther)
				return
			}
		}
		// if the claims permissions doesn't match the routes permissions then return unauthorized
		if !slices.Contains(claims.Permissions, access) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		f(w, r)
	}
}

func (a *Auth) revalidate(w http.ResponseWriter, r *http.Request) (*claims, bool) {
	claims, success := a.getClaims(r, "refresh")
	if !success {
		return nil, false
	}

	// setup signin struct using data from the refesh token
	creds := strings.Split(claims.Subject, "|")
	if len(creds) != 2 {
		a.log.Warn().Msgf("revalidate: claims.Subject had a length != 2")
		return nil, false
	}

	id, err := strconv.Atoi(creds[0])
	if err != nil {
		a.log.Warn().Msgf("revalidate: atoi failed to convert string id to int")
		return nil, false
	}

	sess, err := strconv.Atoi(claims.ID)
	if err != nil {
		a.log.Warn().Msgf("revalidate: atoi failed to convert string sess to int")
		return nil, false
	}

	info := &signin{
		User:    creds[1],
		id:      id,
		session: sess,
	}

	// revalidate permissions with the db
	if err = a.revalidateSecurityInfo(info); err != nil {
		if err == pgx.ErrNoRows {
			a.log.Warn().Msgf("revalidate: %s no longer exists in db", claims.Subject+"|"+claims.ID)
			return nil, false
		}
		a.log.Err(err).Msg("revalidate: error revalidating with the db")
		return nil, false
	}

	// kick off goroutine to update timestamp of last session revalidation
	go func() {
		if err := a.updateSessionTimestamp(info); err != nil {
			a.log.Err(err).Msg("revalidate: error updating session timestamp")
		}
	}()

	// recreate the refesh token using all the original information except for possibly updated permissions.
	claims.Permissions = info.permissions
	if err := a.setAuthCookie(w, "refresh", claims, true); err != nil {
		a.log.Err(err).Msgf("revalidate: failed to create refresh token")
		return nil, false
	}

	// change vars that we want to hide or show differently for the user cookie.
	// change back to old values before writing the access cookie.
	accessSubject := claims.Subject
	accessID := claims.ID
	claims.Subject = info.User
	claims.ID = ""

	// recreate the user token
	if err := a.setAuthCookie(w, "session", claims, false); err != nil {
		a.log.Err(err).Msgf("revalidate: failed to create user token")
		return nil, false
	}

	// recreate the access token
	expirationTime := time.Now().Add(a.config.AccessExpire)
	claims.ExpiresAt = jwt.NewNumericDate(expirationTime)
	claims.Subject = accessSubject
	claims.ID = accessID
	if err := a.setAuthCookie(w, "access", claims, true); err != nil {
		a.log.Err(err).Msgf("revalidate: failed to create access token")
		return nil, false
	}

	// set tracking cookie
	if _, err := r.Cookie("id"); err != nil {
		if err := tracker.CreateAuthTracker(w, info.User, info.permissions); err != nil {
			a.log.Err(err).Msg("revalidate: failed to create tracking token")
			return nil, false
		}
	}

	a.log.Info().Msgf("%s access token refreshed", claims.Subject)

	return claims, true
}

func (a *Auth) getClaims(r *http.Request, cookie string) (*claims, bool) {
	// We can obtain the session token from the requests cookies, which come with every request
	c, err := r.Cookie(cookie)
	if err != nil {
		return nil, false
	}

	// Get the JWT string from the cookie
	tokenStr := c.Value

	// Initialize a new instance of `Claims`
	claims := &claims{}

	// Parse the JWT string and store the result in `claims`.
	// Note that we are passing the key in this method as well. This method will return an error
	// if the token is invalid (if it has expired according to the expiry time we set on sign in),
	// or if the signature does not match
	token, err := jwt.ParseWithClaims(tokenStr, claims, func(token *jwt.Token) (any, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			// verify that the algorith is what we expect.
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return a.secret, nil
	})
	if err != nil {
		if err == jwt.ErrTokenExpired {
			// token probably expired in flight, need to revalidate
			return nil, false
		}
		if err == jwt.ErrSignatureInvalid {
			a.log.Err(err).Msg("invalid signature")
			return nil, false
		}
		a.log.Err(err).Msg("bad request")
		return nil, false
	}
	if !token.Valid {
		a.log.Err(errors.New("jwt.ParseWithClaims returned an invalid token")).Msg("invalid token")
		return nil, false
	}

	return claims, true
}

func (a *Auth) createTokens(w http.ResponseWriter, info *signin) error {
	// declare the expiration time of the token.
	expirationTime := time.Now().Add(a.config.AccessExpire)
	// create the JWT claims, which includes the username and expiry time
	claims := &claims{
		Permissions: info.permissions,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    a.config.Issuer,
			Subject:   strconv.Itoa(info.id) + "|" + info.User,
			ID:        strconv.Itoa(info.session),
			ExpiresAt: jwt.NewNumericDate(expirationTime),
		},
	}

	// set the access cookie
	if err := a.setAuthCookie(w, "access", claims, true); err != nil {
		a.log.Err(err).Msg("createTokens: error setting access cookie")
		return err
	}

	// set the refresh cookie
	claims.ExpiresAt = jwt.NewNumericDate(info.expires)
	if err := a.setAuthCookie(w, "refresh", claims, true); err != nil {
		a.log.Err(err).Msg("createTokens: error setting refresh cookie")
		return err
	}

	// set session cookie
	claims.Subject = info.User
	claims.ID = ""
	if err := a.setAuthCookie(w, "session", claims, false); err != nil {
		a.log.Err(err).Msg("createTokens: error setting session cookie")
		return err
	}

	// set tracking cookie
	if err := tracker.CreateAuthTracker(w, info.User, info.permissions); err != nil {
		a.log.Err(err).Msg("createTokens: error setting tracking cookie")
		return err
	}

	return nil
}

func (a *Auth) setAuthCookie(w http.ResponseWriter, name string, claims *claims, httpOnly bool) error {
	// declare the token with the algorithm used for signing, and the claims.
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	// create the JWT string
	tokenString, err := token.SignedString(a.secret)
	if err != nil {
		// if there is an error in creating the JWT return an internal server error
		w.WriteHeader(http.StatusInternalServerError)
		return err
	}

	// finally, we set the client cookie for "token" as the JWT we just generated
	// we also set an expiry time which is the same as the token itself
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    tokenString,
		Path:     "/",
		Expires:  claims.ExpiresAt.Time,
		Secure:   true,
		HttpOnly: httpOnly,
		SameSite: http.SameSiteLaxMode,
	})

	return nil
}

func (*Auth) deleteCookie(w http.ResponseWriter, name string) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     "/",
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}
