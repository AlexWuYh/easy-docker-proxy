package store

import (
	"context"
	"errors"
	"sync"
	"testing"
)

func TestDeleteUserKeepsAtLeastOneAdminUnderConcurrency(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	first, err := st.CreateUser(ctx, "admin-one", "password-one", RoleAdmin)
	if err != nil {
		t.Fatal(err)
	}
	second, err := st.CreateUser(ctx, "admin-two", "password-two", RoleAdmin)
	if err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, id := range []int64{first.ID, second.ID} {
		wg.Add(1)
		go func(userID int64) {
			defer wg.Done()
			<-start
			results <- st.DeleteUser(ctx, userID)
		}(id)
	}
	close(start)
	wg.Wait()
	close(results)

	var deleted, protected int
	for err := range results {
		switch {
		case err == nil:
			deleted++
		case errors.Is(err, ErrLastAdmin):
			protected++
		default:
			t.Fatalf("unexpected delete result: %v", err)
		}
	}
	if deleted != 1 || protected != 1 {
		t.Fatalf("deleted=%d protected=%d", deleted, protected)
	}

	users, err := st.ListUsers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	admins := 0
	for _, user := range users {
		if user.Role == RoleAdmin {
			admins++
		}
	}
	if admins != 1 {
		t.Fatalf("admins=%d users=%+v", admins, users)
	}
}
