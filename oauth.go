package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
)

type oauthAuthorization struct {
	verifier string
	url      string
}

func newOAuthAuthorization(authorizeURL, clientID, scope, redirectURI, state string, extra url.Values) (oauthAuthorization, error) {
	verifier, err := randomURLValue()
	if err != nil {
		return oauthAuthorization{}, err
	}
	endpoint, err := url.Parse(authorizeURL)
	if err != nil {
		return oauthAuthorization{}, errors.New("invalid authorization URL")
	}
	query := endpoint.Query()
	query.Set("response_type", "code")
	query.Set("client_id", clientID)
	query.Set("redirect_uri", redirectURI)
	query.Set("scope", scope)
	query.Set("code_challenge", pkceChallenge(verifier))
	query.Set("code_challenge_method", "S256")
	query.Set("state", state)
	for name, values := range extra {
		if len(values) > 0 {
			query.Set(name, values[0])
		}
	}
	endpoint.RawQuery = query.Encode()
	return oauthAuthorization{verifier: verifier, url: endpoint.String()}, nil
}

func randomURLValue() (string, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", errors.New("could not generate secure login values")
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func pkceChallenge(verifier string) string {
	hash := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(hash[:])
}

type oauthCallbackServer struct {
	server      *http.Server
	redirectURI string
	state       string
	path        string
	success     string
	result      chan oauthCallbackResult
}

type oauthCallbackResult struct {
	code string
	err  error
}

func startOAuthCallback(address, publicAddress, path, state, success string) (*oauthCallbackServer, error) {
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return nil, err
	}
	if publicAddress == "" {
		publicAddress = listener.Addr().String()
	}
	callback := &oauthCallbackServer{
		redirectURI: oauthCallbackURI(publicAddress, path),
		state:       state,
		path:        path,
		success:     success,
		result:      make(chan oauthCallbackResult, 1),
	}
	callback.server = &http.Server{Handler: http.HandlerFunc(callback.handle)}
	go func() { _ = callback.server.Serve(listener) }()
	return callback, nil
}

func oauthCallbackURI(address, path string) string {
	return "http://" + address + path
}

func (callback *oauthCallbackServer) wait(ctx context.Context) (string, error) {
	select {
	case result := <-callback.result:
		return result.code, result.err
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func (callback *oauthCallbackServer) handle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if r.URL.Path != callback.path {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	state, ok := singleCallbackParameter(r.URL.Query(), "state")
	if !ok || subtle.ConstantTimeCompare([]byte(state), []byte(callback.state)) != 1 {
		callbackError(w, "State mismatch.")
		return
	}
	if _, canceled := r.URL.Query()["error"]; canceled {
		if _, ok := singleCallbackParameter(r.URL.Query(), "error"); !ok {
			callbackError(w, "Invalid callback.")
			return
		}
		if _, hasCode := r.URL.Query()["code"]; hasCode {
			callbackError(w, "Invalid callback.")
			return
		}
		callbackError(w, "Login cancelled.")
		callback.deliver(oauthCallbackResult{err: errors.New("login cancelled")})
		return
	}
	code, ok := singleCallbackParameter(r.URL.Query(), "code")
	if !ok {
		callbackError(w, "Missing authorization code.")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.WriteString(w, callback.success)
	callback.deliver(oauthCallbackResult{code: code})
}

func singleCallbackParameter(values url.Values, name string) (string, bool) {
	value := values[name]
	if len(value) != 1 || value[0] == "" {
		return "", false
	}
	return value[0], true
}

func callbackError(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusBadRequest)
	_, _ = io.WriteString(w, message)
}

func (callback *oauthCallbackServer) deliver(result oauthCallbackResult) {
	select {
	case callback.result <- result:
	default:
	}
}

func postOAuthToken(ctx context.Context, provider, tokenURL, contentType string, body io.Reader) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, body)
	if err != nil {
		return nil, providerFailure(provider, categoryUnavailable, err)
	}
	req.Header.Set("Content-Type", contentType)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, oauthHTTPFailure(provider, err)
	}
	if res.StatusCode == http.StatusBadRequest {
		res.Body.Close()
		return nil, providerFailure(provider, categoryAuthentication, nil)
	}
	if res.StatusCode != http.StatusOK {
		res.Body.Close()
		return nil, providerFailure(provider, classifyHTTPStatus(res.StatusCode), nil)
	}
	return res.Body, nil
}

func oauthHTTPFailure(provider string, err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return providerFailure(provider, categoryTimeout, err)
	}
	if errors.Is(err, context.Canceled) {
		return err
	}
	return providerFailure(provider, categoryUnavailable, err)
}
