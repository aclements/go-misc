// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"

	"golang.org/x/oauth2"
)

// Based on https://github.com/eliben/code-for-blog/blob/main/2024/go-docs-sheets-auth/using-oauth2-auto-token.go

// makeOAuthClient creates a new http.Client with oauth2 set up from the
// given config.
func makeOAuthClient(cacheDir string, config *oauth2.Config) *http.Client {
	tokFile := filepath.Join(cacheDir, "token.json")
	tok, err := loadCachedToken(tokFile)
	if err != nil {
		tok = getTokenFromWeb(config)
		saveCachedToken(tokFile, tok)
	}
	return config.Client(context.Background(), tok)
}

// getTokenFromWeb launches a web browser to authenticate the user vs. Google's
// auth server and returns the token.
func getTokenFromWeb(config *oauth2.Config) *oauth2.Token {
	const redirectPath = "/redirect"
	// We spin up a goroutine with a web server listening on the redirect route,
	// which the auth server will redirect the user's browser to after
	// authentication.
	listener, err := net.Listen("tcp", ":0")
	if err != nil {
		log.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port

	// When the web server receives redirection, it sends the code to codeChan.
	codeChan := make(chan string)
	var srv http.Server

	go func() {
		mux := http.NewServeMux()
		mux.HandleFunc(redirectPath, func(w http.ResponseWriter, req *http.Request) {
			codeChan <- req.URL.Query().Get("code")
			fmt.Fprintf(w, "You may now close this tab.")
		})
		srv.Handler = mux
		if err := srv.Serve(listener); err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()

	config.RedirectURL = fmt.Sprintf("http://localhost:%d%s", port, redirectPath)
	// Use PKCE to protect against CSRF attacks
	verifier := oauth2.GenerateVerifier()
	authURL := config.AuthCodeURL("state-token", oauth2.AccessTypeOffline, oauth2.S256ChallengeOption(verifier))
	fmt.Fprintln(os.Stderr, "Click this link to authenticate:\n", authURL)

	// Receive code from the web server and shut it down.
	authCode := <-codeChan
	if err := srv.Shutdown(context.Background()); err != nil {
		log.Fatal(err)
	}

	// Exchange the auth code for a token (and check the verifier).
	tok, err := config.Exchange(context.Background(), authCode, oauth2.VerifierOption(verifier))
	if err != nil {
		log.Fatalf("unable to retrieve token from web: %v", err)
	}
	return tok
}

// loadCachedToken tries to load a cached token from a local file.
func loadCachedToken(file string) (*oauth2.Token, error) {
	b, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	tok := &oauth2.Token{}
	err = json.Unmarshal(b, &tok)
	return tok, err
}

// saveCachedToken saves an oauth2 token to a local file.
func saveCachedToken(path string, token *oauth2.Token) {
	log.Printf("Saving token to: %s\n", path)
	b, err := json.Marshal(token)
	if err != nil {
		log.Fatal(err)
	}
	err = os.WriteFile(path, b, 0600)
	if err != nil {
		log.Fatalf("unable to cache OAuth token: %v", err)
	}
}
