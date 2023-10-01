package tracker

import (
	"encoding/base64"
	"math/rand"
	"net/http"
	"strconv"
	"time"

	"github.com/cespare/xxhash/v2"
	"github.com/goccy/go-json"
)

// These functions are *NOT* cryptographically secure so should not be used for
// authorization.  Use the JWT access/refresh/session tokens for that.
// The tracking cookie (named "id") can be used for the rate limiter or by the client
// for display only info.

// Info is used to uniquely identify repeat visitors for clients that use cookies.
type Info struct {
	ID    int64    `json:"id"`
	Name  string   `json:"name"`
	Auth  bool     `json:"auth"`
	Scope []string `json:"scope,omitempty"`
}

type payload struct {
	Info *Info  `json:"info"`
	Sig  uint64 `json:"sig"`
}

// GetTrackingInfo will return a valid tracking cookie whether it creates its own or
// returns a previously stored tracking cookie
func GetTrackingInfo(w http.ResponseWriter, r *http.Request) *Info {
	info, err := getTrackingCookie(r)
	if err == nil {
		if info != nil {
			return info
		}
	}

	if err = createAnonTracker(w); err != nil {
		return nil
	}

	return nil
}

// CreateAuthTracker returns a tracking cookie using the users authenticated account name.
func CreateAuthTracker(w http.ResponseWriter, name string, permissions []string) error {
	payload := &payload{
		Info: &Info{
			ID:    rand.Int63(),
			Name:  name,
			Auth:  true,
			Scope: permissions,
		},
	}
	return createNewTracker(w, payload)
}

func getTrackingCookie(r *http.Request) (*Info, error) {
	c, err := r.Cookie("id")
	if err != nil {
		return nil, nil
	}

	val, err := base64.URLEncoding.DecodeString(c.Value)
	if err != nil {
		return nil, err
	}

	payload := &payload{Info: &Info{}}
	if err = json.Unmarshal(val, payload); err != nil {
		return nil, err
	}

	if !validateTrackingCookie(payload) {
		return nil, nil
	}

	return payload.Info, nil
}

func validateTrackingCookie(payload *payload) bool {
	bytes, err := json.Marshal(payload.Info)
	if err != nil {
		return false
	}

	if payload.Sig != xxhash.Sum64(bytes) {
		return false
	}

	return true
}

func createAnonTracker(w http.ResponseWriter) error {
	payload := &payload{
		Info: &Info{
			ID:   rand.Int63(),
			Name: strconv.FormatInt(rand.Int63(), 16)[8:],
			Auth: false,
		},
	}
	return createNewTracker(w, payload)
}

func createNewTracker(w http.ResponseWriter, payload *payload) error {
	bytes, err := json.Marshal(payload.Info)
	if err != nil {
		return err
	}

	payload.Sig = xxhash.Sum64(bytes)

	bytes, err = json.Marshal(payload)
	if err != nil {
		return err
	}

	return writeTrackingCookie(w, bytes)
}

func writeTrackingCookie(w http.ResponseWriter, bytes []byte) error {
	val := base64.URLEncoding.EncodeToString(bytes)
	http.SetCookie(w, &http.Cookie{
		Name:     "id",
		Value:    val,
		Path:     "/",
		Expires:  time.Now().Add(24 * 365 * time.Hour),
		Secure:   true,
		HttpOnly: false,
		SameSite: http.SameSiteLaxMode,
	})

	return nil
}
