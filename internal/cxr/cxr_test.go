package cxr

import (
	"testing"
	"time"
)

func TestParseSessionsKeepsOnlyIDs(t *testing.T) {
	sessions, err := parseSessions([]byte(`[
  {"id":"session-a","updatedAt":"2026-07-29T04:57:41.264Z","firstUserMessage":"private","workingDirectory":"D:\\CodexWork\\example"},
  {"id":"","updatedAt":"2026-07-29T04:57:41.264Z"}
]`))
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 {
		t.Fatalf("sessions = %#v", sessions)
	}
	if sessions[0].ID != "session-a" {
		t.Fatalf("ID = %q", sessions[0].ID)
	}
	if sessions[0].FirstUserMessage != "private" {
		t.Fatalf("FirstUserMessage = %q", sessions[0].FirstUserMessage)
	}
	if sessions[0].WorkingDirectory != `D:\CodexWork\example` {
		t.Fatalf("WorkingDirectory = %q", sessions[0].WorkingDirectory)
	}
	if sessions[0].UpdatedAt.IsZero() || sessions[0].UpdatedAt.Location() != time.UTC {
		t.Fatalf("UpdatedAt = %v", sessions[0].UpdatedAt)
	}
}
