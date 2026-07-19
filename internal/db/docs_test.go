package db

import "testing"

func TestDocsCRUDAndPublish(t *testing.T) {
	d, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	// Migration may seed a default published tutorial; clear so CRUD starts empty.
	if _, err := d.Exec(`DELETE FROM docs`); err != nil {
		t.Fatal(err)
	}

	a, err := CreateDoc(d, "入门", "# hello\n\nbody", false)
	if err != nil {
		t.Fatal(err)
	}
	if a.ID == 0 || a.Published {
		t.Fatalf("unexpected create: %+v", a)
	}

	b, err := CreateDoc(d, "进阶", "more", true)
	if err != nil {
		t.Fatal(err)
	}
	if b.SortOrder <= a.SortOrder || !b.Published {
		t.Fatalf("unexpected second doc: %+v (first %+v)", b, a)
	}

	all, err := ListDocs(d)
	if err != nil || len(all) != 2 {
		t.Fatalf("list all: %v %#v", err, all)
	}
	pub, err := ListPublishedDocs(d)
	if err != nil || len(pub) != 1 || pub[0].ID != b.ID {
		t.Fatalf("list published: %v %#v", err, pub)
	}

	if _, err := UpdateDoc(d, a.ID, "入门改", "updated", true); err != nil {
		t.Fatal(err)
	}
	got, err := GetPublishedDoc(d, a.ID)
	if err != nil || got.Title != "入门改" || got.Content != "updated" {
		t.Fatalf("get published after update: %v %+v", err, got)
	}

	if err := SwapDocOrder(d, a.ID, b.ID); err != nil {
		t.Fatal(err)
	}
	all, _ = ListDocs(d)
	if all[0].ID != b.ID || all[1].ID != a.ID {
		t.Fatalf("swap order failed: %+v", all)
	}

	if err := ReorderDocs(d, []int64{a.ID, b.ID}); err != nil {
		t.Fatal(err)
	}
	all, _ = ListDocs(d)
	if all[0].ID != a.ID || all[0].SortOrder != 0 || all[1].SortOrder != 1 {
		t.Fatalf("reorder failed: %+v", all)
	}

	if err := DeleteDoc(d, b.ID); err != nil {
		t.Fatal(err)
	}
	all, _ = ListDocs(d)
	if len(all) != 1 || all[0].ID != a.ID {
		t.Fatalf("delete failed: %+v", all)
	}
}
