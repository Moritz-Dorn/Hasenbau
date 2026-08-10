package store

import (
	"testing"
)

func TestTraceSchreibenUndLesen(t *testing.T) {
	s := neuerStore(t)

	id, err := s.StartLauf("pdf-einlagern", "watch", "raeume/laderampe/sources/a.pdf")
	if err != nil {
		t.Fatal(err)
	}

	if _, da, err := s.ReadTrace(id); err != nil || da {
		t.Errorf("vor dem Schreiben: da=%v, err=%v", da, err)
	}

	roh := []byte(`{"session_id":"ses_1","steps":[{"kind":"tool","tool":"write"}]}`)
	if err := s.WriteTrace(id, "ses_1", roh); err != nil {
		t.Fatal(err)
	}
	gelesen, da, err := s.ReadTrace(id)
	if err != nil || !da {
		t.Fatalf("nach dem Schreiben: da=%v, err=%v", da, err)
	}
	if string(gelesen) != string(roh) {
		t.Errorf("gelesen = %q", gelesen)
	}

	// Nachtragen darf überschreiben — der Lazy-Backfill in `graben`
	// fragt nicht erst.
	neu := []byte(`{"session_id":"ses_1","steps":[]}`)
	if err := s.WriteTrace(id, "ses_1", neu); err != nil {
		t.Fatal(err)
	}
	gelesen, _, err = s.ReadTrace(id)
	if err != nil {
		t.Fatal(err)
	}
	if string(gelesen) != string(neu) {
		t.Errorf("nach dem Nachtragen = %q", gelesen)
	}
}

func TestTraceOhneSessionIDAbgelehnt(t *testing.T) {
	s := neuerStore(t)
	id, err := s.StartLauf("pdf-einlagern", "manual", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.WriteTrace(id, "", []byte(`{}`)); err == nil {
		t.Error("Trace ohne Session-ID muss ein Fehler sein")
	}
}

func TestTraceUnbekannterLauf(t *testing.T) {
	s := neuerStore(t)
	if _, da, err := s.ReadTrace(99); err != nil || da {
		t.Errorf("unbekannter Lauf: da=%v, err=%v", da, err)
	}
	// Die Fremdschlüssel-Bedingung greift (foreign_keys ist an).
	if err := s.WriteTrace(99, "ses_x", []byte(`{}`)); err == nil {
		t.Error("Trace zu einem Lauf, den es nicht gibt, muss scheitern")
	}
}
