package htmx

import (
	"errors"
	"net/http"
	"testing"
	"time"
)

func TestAttrsBuildZeroEmitsNothing(t *testing.T) {
	out, err := Attrs{}.Build()
	if err != nil {
		t.Fatalf("zero Attrs.Build: %v", err)
	}
	if len(out) != 0 {
		t.Errorf("zero Attrs should emit nothing, got %v", out)
	}
}

func TestAttrsBuildValid(t *testing.T) {
	out, err := Attrs{
		Method:  MethodPost,
		URL:     "/articles",
		Target:  "#list",
		Swap:    SwapOuterHTML,
		PushURL: true,
	}.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	want := map[string]string{
		"hx-post":     "/articles",
		"hx-target":   "#list",
		"hx-swap":     "outerHTML",
		"hx-push-url": "true",
	}
	for k, v := range want {
		if out[k] != v {
			t.Errorf("%s = %v, want %v", k, out[k], v)
		}
	}
}

func TestAttrsBuildInvalid(t *testing.T) {
	tests := []struct {
		name  string
		attrs Attrs
	}{
		{"method without url", Attrs{Method: MethodGet}},
		{"unknown method", Attrs{Method: Method("connect"), URL: "/x"}},
		{"unknown swap", Attrs{Swap: Swap("teleport")}},
		{"control char in url", Attrs{Method: MethodGet, URL: "/x\x00"}},
		{"control char in target", Attrs{Target: "#a\x00"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := tt.attrs.Build()
			if !errors.Is(err, ErrInvalidAttrs) {
				t.Fatalf("Build err = %v, want ErrInvalidAttrs", err)
			}
			if out != nil {
				t.Errorf("Build returned a non-nil attribute set alongside an error: %v", out)
			}
		})
	}
}

// TestAttrsTriggerModifiers proves the GOTH-5.3-frozen typed Trigger emits the
// debounced "changed" modifier both real consumers need (Combobox async, Data
// Table live filter), and errors on modifiers without an event.
func TestAttrsTriggerModifiers(t *testing.T) {
	out, err := Attrs{
		Method:  MethodGet,
		URL:     "/rows",
		Target:  "#rows",
		Trigger: Trigger{Event: "keyup", Changed: true, Delay: 300 * time.Millisecond},
	}.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if got := out["hx-trigger"]; got != "keyup changed delay:300ms" {
		t.Errorf("hx-trigger = %v, want %q", got, "keyup changed delay:300ms")
	}

	throttled, err := Attrs{Trigger: Trigger{Event: "input", Throttle: 500 * time.Millisecond}}.Build()
	if err != nil {
		t.Fatalf("Build throttle: %v", err)
	}
	if got := throttled["hx-trigger"]; got != "input throttle:500ms" {
		t.Errorf("hx-trigger = %v, want %q", got, "input throttle:500ms")
	}

	if (Trigger{}).IsZero() != true {
		t.Error("zero Trigger should be IsZero")
	}
	if _, err := (Attrs{Trigger: Trigger{Changed: true}}).Build(); !errors.Is(err, ErrInvalidAttrs) {
		t.Error("trigger modifiers without an event should error")
	}
}

// TestAttrsSwapModifiers proves the GOTH-5.3-frozen swap modifiers append after
// the strategy (the Data Table preserves scroll/focus on a content swap).
func TestAttrsSwapModifiers(t *testing.T) {
	no := false
	out, err := Attrs{
		Swap:     SwapOuterHTML,
		SwapMods: SwapModifiers{Show: "none", FocusScroll: &no},
	}.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if got := out["hx-swap"]; got != "outerHTML show:none focus-scroll:false" {
		t.Errorf("hx-swap = %v, want %q", got, "outerHTML show:none focus-scroll:false")
	}

	// Modifiers with no explicit strategy default to innerHTML.
	def, err := Attrs{SwapMods: SwapModifiers{Show: "none"}}.Build()
	if err != nil {
		t.Fatalf("Build default: %v", err)
	}
	if got := def["hx-swap"]; got != "innerHTML show:none" {
		t.Errorf("hx-swap = %v, want %q", got, "innerHTML show:none")
	}

	if _, err := (Attrs{Swap: SwapOuterHTML, SwapMods: SwapModifiers{Show: "x\x00"}}).Build(); !errors.Is(err, ErrInvalidAttrs) {
		t.Error("control char in swap modifier should error")
	}
}

func TestMethodAndSwapValid(t *testing.T) {
	if Method("").Valid() {
		t.Errorf("empty Method should not be Valid")
	}
	if !MethodDelete.Valid() {
		t.Errorf("MethodDelete should be Valid")
	}
	if !SwapInnerHTML.Valid() {
		t.Errorf("SwapInnerHTML should be Valid")
	}
	if Swap("nope").Valid() {
		t.Errorf("unknown Swap should not be Valid")
	}
}

func TestFromRequest(t *testing.T) {
	t.Run("non-htmx", func(t *testing.T) {
		r, _ := http.NewRequest(http.MethodGet, "/x", nil)
		rq := FromRequest(r)
		if rq.IsHTMX() {
			t.Errorf("plain request should not be HTMX")
		}
	})
	t.Run("htmx hints", func(t *testing.T) {
		r, _ := http.NewRequest(http.MethodGet, "/x", nil)
		r.Header.Set("HX-Request", "true")
		r.Header.Set("HX-Target", "list")
		r.Header.Set("HX-Trigger-Name", "search")
		rq := FromRequest(r)
		if !rq.IsHTMX() {
			t.Errorf("IsHTMX should be true")
		}
		if rq.Target() != "list" {
			t.Errorf("Target = %q, want list", rq.Target())
		}
		if rq.TriggerName() != "search" {
			t.Errorf("TriggerName = %q, want search", rq.TriggerName())
		}
	})
	t.Run("nil request", func(t *testing.T) {
		if FromRequest(nil).IsHTMX() {
			t.Errorf("nil request should yield a zero Request")
		}
	})
}
