package main

import "testing"

func TestBind_SQLitePassthrough(t *testing.T) {
	q := "SELECT id FROM t WHERE channel = ? AND id > ?"
	if got := bind(q, false); got != q {
		t.Fatalf("expected passthrough, got %q", got)
	}
}

func TestBind_PostgresNumbersPlaceholders(t *testing.T) {
	got := bind("SELECT id FROM t WHERE channel = ? AND id > ?", true)
	want := "SELECT id FROM t WHERE channel = $1 AND id > $2"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}
