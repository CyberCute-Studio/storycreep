package database

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestScramVerifier(t *testing.T) {
	for _, password := range []string{"pencil", "pässwörd", "", "with ' quote", "\x07bell"} {
		v, err := scramVerifier(password)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(v, "SCRAM-SHA-256$4096:") || strings.Contains(v, password) && password != "" {
			t.Errorf("%q: verifier %q", password, v)
		}
		if !scramMatches(v, password) {
			t.Errorf("%q: the verifier does not match its password", password)
		}
		if scramMatches(v, password+"x") {
			t.Errorf("%q: the verifier matches another password", password)
		}
		if again, _ := scramVerifier(password); again == v {
			t.Errorf("%q: two verifiers share a salt", password)
		}
	}
}

// TestScramVector checks the verifier against RFC 7677's example, whose
// password is "pencil": the server signature it gives is the HMAC of the
// exchange under the server key, so the server key decides it.
func TestScramVector(t *testing.T) {
	salt, _ := base64.StdEncoding.DecodeString("W22ZaJ0SNY7soEsUEjb6gQ==")
	_, serverKey, err := scramKeys("pencil", salt, 4096)
	if err != nil {
		t.Fatal(err)
	}
	authMessage := "n=user,r=rOprNGfwEbeRWgbNEkqO,r=rOprNGfwEbeRWgbNEkqO%hvYDpWUa2RaTCAfuxFIlj)hNlF$k0," +
		"s=W22ZaJ0SNY7soEsUEjb6gQ==,i=4096,c=biws,r=rOprNGfwEbeRWgbNEkqO%hvYDpWUa2RaTCAfuxFIlj)hNlF$k0"
	got := base64.StdEncoding.EncodeToString(hmacSHA256(serverKey, authMessage))
	if want := "6rriTRBi23WpRR/wtup+mMhUZUn/dB5nLTJRsjl95G4="; got != want {
		t.Errorf("server signature %s, want %s", got, want)
	}
}

func TestScramMatchesRejectsOtherForms(t *testing.T) {
	v, err := scramVerifierWith("pencil", []byte("0123456789abcdef"), 4096)
	if err != nil {
		t.Fatal(err)
	}
	for _, stored := range []string{
		"",
		"md5" + strings.Repeat("0", 32),
		strings.Replace(v, "SCRAM-SHA-256", "SCRAM-SHA-1", 1),
		strings.Replace(v, "$4096:", "$0:", 1),
		strings.Replace(v, "$4096:", "$x:", 1),
		v[:strings.LastIndexByte(v, ':')],
		strings.Replace(v, "$", "", 2),
		v + "!",
	} {
		if scramMatches(stored, "pencil") {
			t.Errorf("%q matches", stored)
		}
	}
	if !scramMatches(v, "pencil") {
		t.Errorf("the verifier does not match")
	}
}
