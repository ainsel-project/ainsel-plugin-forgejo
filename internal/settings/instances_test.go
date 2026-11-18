package settings

import (
	"context"
	"errors"
	"testing"
)

// TestListInstancesEmpty checks the nothing-configured state: an empty, non-nil
// list and no error. The wizard reads this as "no instance to pick".
func TestListInstancesEmpty(t *testing.T) {
	s := openStore(t)

	out, err := s.ListInstances(context.Background())
	if err != nil {
		t.Fatalf("ListInstances: %v", err)
	}
	if out == nil {
		t.Fatal("ListInstances returned nil, want an empty list")
	}
	if len(out) != 0 {
		t.Fatalf("ListInstances returned %d instances, want 0", len(out))
	}
}

// TestSaveInstancesRoundTripInOrder checks the whole-list write and its read
// back: the rows come out in the order they went in, which is the order the
// Settings page showed and the wizard's select draws.
func TestSaveInstancesRoundTripInOrder(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	in := []Instance{
		{Name: "Primary", BaseURL: "https://forge.example.com"},
		{Name: "Lab", BaseURL: "https://lab.example.com/"},
	}
	if err := s.SaveInstances(ctx, in); err != nil {
		t.Fatalf("SaveInstances: %v", err)
	}

	out, err := s.ListInstances(ctx)
	if err != nil {
		t.Fatalf("ListInstances: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("got %d instances, want 2", len(out))
	}
	if out[0].Name != "Primary" || out[0].BaseURL != "https://forge.example.com" {
		t.Errorf("first = %+v, want Primary at https://forge.example.com", out[0])
	}
	// The trailing slash is stored off, so the plan's comparison and the
	// console's read agree on one spelling.
	if out[1].Name != "Lab" || out[1].BaseURL != "https://lab.example.com" {
		t.Errorf("second = %+v, want Lab at https://lab.example.com", out[1])
	}
}

// TestSaveInstancesReplacesWholeList checks the PUT rule: a second write is the
// whole list, not an append — the rows the first write left are gone.
func TestSaveInstancesReplacesWholeList(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	if err := s.SaveInstances(ctx, []Instance{
		{Name: "Old", BaseURL: "https://old.example.com"},
		{Name: "Gone", BaseURL: "https://gone.example.com"},
	}); err != nil {
		t.Fatalf("SaveInstances (first): %v", err)
	}
	if err := s.SaveInstances(ctx, []Instance{
		{Name: "New", BaseURL: "https://new.example.com"},
	}); err != nil {
		t.Fatalf("SaveInstances (second): %v", err)
	}

	out, err := s.ListInstances(ctx)
	if err != nil {
		t.Fatalf("ListInstances: %v", err)
	}
	if len(out) != 1 || out[0].Name != "New" {
		t.Fatalf("got %+v, want exactly the one new instance", out)
	}
}

// TestSaveInstancesEmptyListIsLegal checks that clearing the list is a legal
// write: the admin removed every instance, and the wizard then offers nothing.
func TestSaveInstancesEmptyListIsLegal(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	if err := s.SaveInstances(ctx, []Instance{{Name: "X", BaseURL: "https://x.example.com"}}); err != nil {
		t.Fatalf("SaveInstances: %v", err)
	}
	if err := s.SaveInstances(ctx, nil); err != nil {
		t.Fatalf("SaveInstances(nil): %v", err)
	}

	out, err := s.ListInstances(ctx)
	if err != nil {
		t.Fatalf("ListInstances: %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("got %d instances, want the list emptied", len(out))
	}
}

// TestSaveInstancesRefusesBadRows checks the field-level refusal: a missing
// name, a missing address, and a relative address each name their row and
// field, and nothing is written.
func TestSaveInstancesRefusesBadRows(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	cases := []struct {
		name      string
		in        Instance
		wantField string
	}{
		{"no name", Instance{BaseURL: "https://a.example.com"}, "instances[0].name"},
		{"no url", Instance{Name: "A"}, "instances[0].baseUrl"},
		{"relative url", Instance{Name: "A", BaseURL: "a.example.com"}, "instances[0].baseUrl"},
		{"no host", Instance{Name: "A", BaseURL: "https://"}, "instances[0].baseUrl"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := s.SaveInstances(ctx, []Instance{c.in})
			var invalid *ValidationError
			if !errors.As(err, &invalid) {
				t.Fatalf("SaveInstances = %v, want a ValidationError", err)
			}
			if len(invalid.Fields) != 1 || invalid.Fields[0].Field != c.wantField {
				t.Fatalf("fields = %+v, want one error on %s", invalid.Fields, c.wantField)
			}
			out, err := s.ListInstances(ctx)
			if err != nil {
				t.Fatalf("ListInstances: %v", err)
			}
			if len(out) != 0 {
				t.Fatalf("a refused write left %d rows", len(out))
			}
		})
	}
}

// TestSaveInstancesRefusesDuplicateAddress checks that two rows naming the same
// address are refused, the second one named, with the two spellings that differ
// only by a trailing slash counting as the same address.
func TestSaveInstancesRefusesDuplicateAddress(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	err := s.SaveInstances(ctx, []Instance{
		{Name: "A", BaseURL: "https://a.example.com"},
		{Name: "B", BaseURL: "https://a.example.com/"},
	})
	var invalid *ValidationError
	if !errors.As(err, &invalid) {
		t.Fatalf("SaveInstances = %v, want a ValidationError", err)
	}
	if len(invalid.Fields) != 1 || invalid.Fields[0].Field != "instances[1].baseUrl" {
		t.Fatalf("fields = %+v, want one error naming instances[1].baseUrl", invalid.Fields)
	}
}

// TestSaveInstancesAllOrNothing checks that a bad row anywhere leaves the list
// that was already stored untouched — the same rule Save applies to settings.
func TestSaveInstancesAllOrNothing(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	if err := s.SaveInstances(ctx, []Instance{{Name: "Kept", BaseURL: "https://kept.example.com"}}); err != nil {
		t.Fatalf("SaveInstances: %v", err)
	}

	err := s.SaveInstances(ctx, []Instance{
		{Name: "Good", BaseURL: "https://good.example.com"},
		{Name: "", BaseURL: "https://bad.example.com"},
	})
	var invalid *ValidationError
	if !errors.As(err, &invalid) {
		t.Fatalf("SaveInstances = %v, want a ValidationError", err)
	}

	out, err := s.ListInstances(ctx)
	if err != nil {
		t.Fatalf("ListInstances: %v", err)
	}
	if len(out) != 1 || out[0].Name != "Kept" {
		t.Fatalf("got %+v, want the previously stored list untouched", out)
	}
}

// TestSeedInstanceFromEnv checks the migration: the env address becomes the one
// instance the first time, named by its host; a second start does not re-seed;
// an empty env writes nothing.
func TestSeedInstanceFromEnv(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	// Empty env: nothing to seed, and no error.
	if seeded, err := s.SeedInstanceFromEnv(ctx, ""); err != nil || seeded {
		t.Fatalf("SeedInstanceFromEnv(\"\") = %v, %v, want false, nil", seeded, err)
	}

	seeded, err := s.SeedInstanceFromEnv(ctx, "https://forge.example.com/")
	if err != nil {
		t.Fatalf("SeedInstanceFromEnv: %v", err)
	}
	if !seeded {
		t.Fatal("first seed reported nothing seeded")
	}
	out, err := s.ListInstances(ctx)
	if err != nil {
		t.Fatalf("ListInstances: %v", err)
	}
	if len(out) != 1 || out[0].BaseURL != "https://forge.example.com" {
		t.Fatalf("got %+v, want the env address as the one instance", out)
	}
	if out[0].Name != "forge.example.com" {
		t.Errorf("name = %q, want the host to name the row", out[0].Name)
	}

	// A second start with the env set does not re-seed: the admin's list is
	// theirs from here.
	if seeded, err := s.SeedInstanceFromEnv(ctx, "https://other.example.com"); err != nil || seeded {
		t.Fatalf("second SeedInstanceFromEnv = %v, %v, want false, nil", seeded, err)
	}
	out, _ = s.ListInstances(ctx)
	if len(out) != 1 || out[0].BaseURL != "https://forge.example.com" {
		t.Fatalf("re-seed changed the list to %+v", out)
	}

	// An admin who emptied the list is not re-seeded: the marker says the list
	// has been written, and the env var is the migration, not the owner.
	if err := s.SaveInstances(ctx, nil); err != nil {
		t.Fatalf("SaveInstances(nil): %v", err)
	}
	if seeded, err := s.SeedInstanceFromEnv(ctx, "https://forge.example.com"); err != nil || seeded {
		t.Fatalf("SeedInstanceFromEnv after admin emptied the list = %v, %v, want false, nil", seeded, err)
	}
	out, _ = s.ListInstances(ctx)
	if len(out) != 0 {
		t.Fatalf("re-seed after an admin emptied the list wrote %+v", out)
	}
}

// TestNormalizeBaseURL checks the comparison form: trim, trailing slashes off,
// and nothing else rewritten — a subpath stays, because it is a different
// address.
func TestNormalizeBaseURL(t *testing.T) {
	cases := []struct{ in, want string }{
		{"https://a.example.com", "https://a.example.com"},
		{"  https://a.example.com  ", "https://a.example.com"},
		{"https://a.example.com/", "https://a.example.com"},
		{"https://a.example.com///", "https://a.example.com"},
		{"https://a.example.com/forgejo/", "https://a.example.com/forgejo"},
		{"", ""},
		{"   ", ""},
	}
	for _, c := range cases {
		if got := NormalizeBaseURL(c.in); got != c.want {
			t.Errorf("NormalizeBaseURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
