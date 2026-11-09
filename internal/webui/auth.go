package webui

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"birdie/internal/bus"
	"birdie/internal/store"
)

// Signing in: `birdie ui` saves a one-time code in the bus's database and opens
// the app with it in the URL fragment. The app trades it for a session cookie.
// Only hashes of the code and the session are stored.
const (
	cookieName   = "birdie_ui"
	loginPrefix  = "ui_login:"
	sessPrefix   = "ui_session:"
	loginTTL     = 10 * time.Minute
	sessionTTL   = 30 * 24 * time.Hour
	maxLoginBody = 4 << 10
)

func secret() string {
	b := make([]byte, 32)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func hash(v string) string {
	sum := sha256.Sum256([]byte(v))
	return hex.EncodeToString(sum[:])
}

// NewLoginCode saves a code that signs in to the web app once, within ten
// minutes.
func NewLoginCode(ctx context.Context, st *store.SQLite) (string, error) {
	code := secret()
	return code, st.SetState(ctx, loginPrefix+hash(code), strconv.FormatInt(time.Now().Add(loginTTL).Unix(), 10))
}

func live(v string) bool {
	until, err := strconv.ParseInt(v, 10, 64)
	return err == nil && time.Now().Unix() < until
}

func (s *server) login(w http.ResponseWriter, r *http.Request) {
	var in struct{ Code string }
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxLoginBody)).Decode(&in); err != nil || in.Code == "" {
		s.fail(w, http.StatusBadRequest, "a code is required")
		return
	}
	ctx := r.Context()
	v, err := s.Store.TakeState(ctx, loginPrefix+hash(in.Code))
	if errors.Is(err, bus.ErrNotFound) || (err == nil && !live(v)) {
		s.fail(w, http.StatusUnauthorized, "this sign-in link has expired or was already used; run birdie ui again")
		return
	} else if err != nil {
		s.internal(w, err)
		return
	}
	token := secret()
	expires := time.Now().Add(sessionTTL)
	if err := s.Store.SetState(ctx, sessPrefix+hash(token), strconv.FormatInt(expires.Unix(), 10)); err != nil {
		s.internal(w, err)
		return
	}
	s.Store.DeleteExpiredState(ctx, sessPrefix, time.Now())
	s.Store.DeleteExpiredState(ctx, loginPrefix, time.Now())
	http.SetCookie(w, &http.Cookie{
		Name: cookieName, Value: token, Path: "/ui", Expires: expires, MaxAge: int(sessionTTL.Seconds()),
		HttpOnly: true, SameSite: http.SameSiteStrictMode,
	})
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(cookieName); err == nil {
		s.Store.DeleteState(r.Context(), sessPrefix+hash(c.Value))
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/ui", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode})
	w.WriteHeader(http.StatusNoContent)
}

// signedIn lets a request through only with a live session cookie.
func (s *server) signedIn(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(cookieName)
		if err != nil || c.Value == "" {
			s.fail(w, http.StatusUnauthorized, "sign in by running birdie ui")
			return
		}
		v, err := s.Store.GetState(r.Context(), sessPrefix+hash(c.Value))
		if err != nil || !live(v) {
			s.fail(w, http.StatusUnauthorized, "sign in by running birdie ui")
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		h(w, r)
	}
}

func (s *server) me(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"signed_in": true, "me": Me})
}
