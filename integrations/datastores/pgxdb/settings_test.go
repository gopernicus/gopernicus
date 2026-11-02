package pgxdb

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/sdk"
)

// TestTransactWithSettings_Refusals needs no database: the *DB has no pool, so
// any refusal that reached Begin would panic. Every refusal is ErrInvalidInput
// and fn never runs, for both settings functions.
func TestTransactWithSettings_Refusals(t *testing.T) {
	db := &DB{}
	cases := map[string][]Setting{
		"empty slice":             nil,
		"core role":               {{Name: "role", Value: "x"}},
		"core search_path":        {{Name: "search_path", Value: "x"}},
		"core statement_timeout":  {{Name: "statement_timeout", Value: "1"}},
		"uppercase":               {{Name: "App.X", Value: "x"}},
		"quoted":                  {{Name: `"app.x"`, Value: "x"}},
		"empty part after dot":    {{Name: "app.", Value: "x"}},
		"empty part before dot":   {{Name: ".x", Value: "x"}},
		"three parts":             {{Name: "a.b.c", Value: "x"}},
		"punctuation":             {{Name: "app.x$", Value: "x"}},
		"empty name":              {{Name: "", Value: "x"}},
		"empty value":             {{Name: "app.x", Value: ""}},
		"NUL in value":            {{Name: "app.x", Value: "a\x00b"}},
		"invalid UTF-8 in value":  {{Name: "app.x", Value: "a\xffb"}},
		"duplicate names":         {{Name: "app.x", Value: "1"}, {Name: "app.x", Value: "2"}},
		"one bad among good ones": {{Name: "app.x", Value: "1"}, {Name: "role", Value: "2"}},
	}
	variants := map[string]func(context.Context, []Setting, func(context.Context) error) error{
		"TransactWithSettings":         db.TransactWithSettings,
		"TransactSnapshotWithSettings": db.TransactSnapshotWithSettings,
	}
	for variant, transact := range variants {
		for name, settings := range cases {
			t.Run(variant+"/"+name, func(t *testing.T) {
				ran := false
				err := transact(t.Context(), settings, func(context.Context) error {
					ran = true
					return nil
				})
				if !errors.Is(err, sdk.ErrInvalidInput) {
					t.Fatalf("%s(%q) = %v, want ErrInvalidInput", variant, settings, err)
				}
				if ran {
					t.Fatal("fn ran after a refused setting")
				}
				if len(settings) == 0 && !strings.Contains(err.Error(), variant) {
					t.Fatalf("empty-list refusal %q does not name %s", err, variant)
				}
			})
		}
	}
}

// TestValidateSettings_AcceptedNames pins the guardrail's reach: plpgsql.x is
// accepted because extension prefixes are not blocked, as Setting documents.
func TestValidateSettings_AcceptedNames(t *testing.T) {
	for _, name := range []string{"app.tenant_id", "_a._b", "plpgsql.x"} {
		applied, err := validateSettings("TransactWithSettings", []Setting{{Name: name, Value: "v"}})
		if err != nil {
			t.Fatalf("validateSettings(%q) = %v, want accepted", name, err)
		}
		if applied[name] != "v" {
			t.Fatalf("validateSettings(%q) recorded %q, want %q", name, applied[name], "v")
		}
	}
}

// TestTxSetting_Absent: a transaction begun by Transact or TransactSnapshot
// records no settings, and a recorded transaction reports only the names it
// applied.
func TestTxSetting_Absent(t *testing.T) {
	if v, ok := (&Tx{}).Setting("app.tenant_id"); ok || v != "" {
		t.Fatalf("Setting on a Transact transaction = (%q, %t), want (\"\", false)", v, ok)
	}
	tx := &Tx{settings: map[string]string{"app.tenant_id": "a"}}
	if v, ok := tx.Setting("app.tenant_id"); !ok || v != "a" {
		t.Fatalf("Setting(applied) = (%q, %t), want (\"a\", true)", v, ok)
	}
	if v, ok := tx.Setting("app.actor_id"); ok || v != "" {
		t.Fatalf("Setting(not applied) = (%q, %t), want (\"\", false)", v, ok)
	}
}

func TestSetConfigStatement(t *testing.T) {
	query, args := setConfigStatement([]Setting{{Name: "app.a", Value: "1"}, {Name: "app.b", Value: "2"}})
	if want := "SELECT set_config($1, $2, true), set_config($3, $4, true)"; query != want {
		t.Fatalf("query = %q, want %q", query, want)
	}
	if len(args) != 4 || args[0] != "app.a" || args[1] != "1" || args[2] != "app.b" || args[3] != "2" {
		t.Fatalf("args = %v, want [app.a 1 app.b 2]", args)
	}
}
