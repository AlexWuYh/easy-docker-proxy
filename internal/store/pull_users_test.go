package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/alex_wuyh/easy-docker-proxy/internal/config"
)

func TestPullUsersAuth(t *testing.T) {
	dsn := "file:" + filepath.Join(t.TempDir(), "p.db")
	st, err := Open(config.StorageConfig{Driver: "sqlite", DSN: dsn})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	created, err := st.BootstrapPullUser(context.Background(), "puller", "password1")
	if err != nil || !created {
		t.Fatalf("bootstrap %v created=%v", err, created)
	}
	created2, err := st.BootstrapPullUser(context.Background(), "other", "password1")
	if err != nil || created2 {
		t.Fatalf("second bootstrap should no-op: %v %v", err, created2)
	}

	name, err := st.AuthenticatePull(context.Background(), "puller", "password1")
	if err != nil || name != "puller" {
		t.Fatalf("auth %q %v", name, err)
	}
	if _, err := st.AuthenticatePull(context.Background(), "puller", "wrongpassx"); err == nil {
		t.Fatal("expected fail")
	}

	list, err := st.ListPullUsers(context.Background())
	if err != nil || len(list) != 1 {
		t.Fatalf("list %v %v", list, err)
	}
	if err := st.SetPullUserEnabled(context.Background(), list[0].ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AuthenticatePull(context.Background(), "puller", "password1"); err == nil {
		t.Fatal("disabled user should fail")
	}
}
