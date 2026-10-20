package onepassword_test

import (
	"testing"

	"github.com/leeovery/kit/internal/kind/secret/onepassword"
)

// 1Password's references: a value's is a vault, an item and a field (a
// section's field as section/field), an item's a vault and an item, each
// part there.
func TestReferences(t *testing.T) {
	d := onepassword.Driver{}
	for _, c := range []struct {
		ref           string
		isRef, isItem bool
	}{
		{"op://vault/item/field", true, false},
		{"op://vault/Some Item/Section/field", true, false},
		{"op://vault/item", false, true},
		{"op://vault/", false, false},
		{"op://vault//field", false, false},
		{"op://vault/item/", false, false},
		{"vault/item/field", false, false},
		{"GitHub/token", false, false},
	} {
		if got := d.IsRef(c.ref); got != c.isRef {
			t.Errorf("IsRef(%q) = %v", c.ref, got)
		}
		if got := d.IsItem(c.ref); got != c.isItem {
			t.Errorf("IsItem(%q) = %v", c.ref, got)
		}
	}
	if got := d.Join("op://vault/item", "Section/field"); got != "op://vault/item/Section/field" {
		t.Errorf("Join() = %q", got)
	}
	if short, ok := d.Short("op://vault/item", "op://vault/item/Section/field"); !ok || short != "Section/field" {
		t.Errorf("Short() = %q, %v", short, ok)
	}
	if _, ok := d.Short("op://vault/item", "op://vault/other/field"); ok {
		t.Error("Short() of another item's value = ok")
	}
}

// A field is a section's name and a field's, an attachment a name with no
// /: each checked without asking 1Password, as Store and Attach take them.
func TestFieldsAndAttachments(t *testing.T) {
	d := onepassword.Driver{}
	for field, ok := range map[string]bool{"GitHub/token": true, "nosection": false, "/token": false, "GitHub/": false, "op://vault/item/x": false, "a/b/c": false} {
		if short, err := d.Field(field); (err == nil) != ok || ok && short != field {
			t.Errorf("Field(%q) = %q, %v", field, short, err)
		}
	}
	for name, ok := range map[string]bool{"token.json": true, "": false, "dir/token.json": false} {
		if short, err := d.Attachment(name); (err == nil) != ok || ok && short != name {
			t.Errorf("Attachment(%q) = %q, %v", name, short, err)
		}
	}
}
