package pgxdb

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

// ---------------------------------------------------------------------------
// QuoteIdentifier
// ---------------------------------------------------------------------------

func TestQuoteIdentifier_SimpleColumn(t *testing.T) {
	result, err := QuoteIdentifier("user_id")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if result != `"user_id"` {
		t.Errorf("got %q, want %q", result, `"user_id"`)
	}
}

func TestQuoteIdentifier_SchemaTable(t *testing.T) {
	result, err := QuoteIdentifier("public.users")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if result != `"public"."users"` {
		t.Errorf("got %q, want %q", result, `"public"."users"`)
	}
}

func TestQuoteIdentifier_WithAlias(t *testing.T) {
	result, err := QuoteIdentifier("created_at timestamp")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if result != `"created_at" "timestamp"` {
		t.Errorf("got %q, want %q", result, `"created_at" "timestamp"`)
	}
}

func TestQuoteIdentifier_SchemaTableWithAlias(t *testing.T) {
	result, err := QuoteIdentifier("public.users u")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if result != `"public"."users" "u"` {
		t.Errorf("got %q, want %q", result, `"public"."users" "u"`)
	}
}

func TestQuoteIdentifier_RejectsDangerousChars(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"semicolon", "users; DROP TABLE users"},
		{"single quote", "users'--"},
		{"double quote", `users"--`},
		{"backslash", `users\x00`},
		{"parentheses", "users()"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := QuoteIdentifier(tt.input)
			if err == nil {
				t.Errorf("QuoteIdentifier(%q) err = nil, want error", tt.input)
				return
			}
			if !strings.Contains(err.Error(), "dangerous characters") {
				t.Errorf("err = %q, want containing %q", err.Error(), "dangerous characters")
			}
		})
	}
}

func TestQuoteIdentifier_RejectsInvalidFormats(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"starts with number", "1users"},
		{"too many parts", "a b c"},
		{"too many segments", "a.b.c.d"},
		{"empty string", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := QuoteIdentifier(tt.input)
			if err == nil {
				t.Errorf("QuoteIdentifier(%q) err = nil, want error", tt.input)
			}
		})
	}
}

func TestQuoteIdentifier_AcceptsValid(t *testing.T) {
	for _, input := range []string{"id", "user_id", "createdAt", "column123", "a"} {
		t.Run(input, func(t *testing.T) {
			result, err := QuoteIdentifier(input)
			if err != nil {
				t.Fatalf("QuoteIdentifier(%q) err = %v", input, err)
			}
			if result == "" {
				t.Errorf("result is empty")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// determineOperator
// ---------------------------------------------------------------------------

func TestDetermineOperator(t *testing.T) {
	tests := []struct {
		name        string
		direction   string
		forPrevious bool
		want        string
	}{
		{"ASC next page", "ASC", false, ">"},
		{"ASC previous page", "ASC", true, "<"},
		{"DESC next page", "DESC", false, "<"},
		{"DESC previous page", "DESC", true, ">"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := determineOperator(tt.direction, tt.forPrevious)
			if got != tt.want {
				t.Errorf("determineOperator(%q, %v) = %q, want %q", tt.direction, tt.forPrevious, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// AddOrderByClause
// ---------------------------------------------------------------------------

func TestAddOrderByClause_ASC(t *testing.T) {
	buf := &bytes.Buffer{}
	buf.WriteString("SELECT * FROM users")

	if err := AddOrderByClause(buf, "created_at", "user_id", "ASC", false); err != nil {
		t.Fatalf("err = %v", err)
	}

	result := buf.String()
	if !strings.Contains(result, `ORDER BY "created_at" ASC`) {
		t.Errorf("missing ORDER BY ASC clause in: %s", result)
	}
	if !strings.Contains(result, `"user_id" ASC`) {
		t.Errorf("missing secondary sort in: %s", result)
	}
}

func TestAddOrderByClause_DESC(t *testing.T) {
	buf := &bytes.Buffer{}
	buf.WriteString("SELECT * FROM users")

	if err := AddOrderByClause(buf, "created_at", "user_id", "DESC", false); err != nil {
		t.Fatalf("err = %v", err)
	}

	result := buf.String()
	if !strings.Contains(result, `ORDER BY "created_at" DESC`) {
		t.Errorf("missing ORDER BY DESC clause in: %s", result)
	}
	if !strings.Contains(result, `"user_id" DESC`) {
		t.Errorf("missing secondary sort in: %s", result)
	}
}

func TestAddOrderByClause_ReversesForPrevious(t *testing.T) {
	buf := &bytes.Buffer{}
	buf.WriteString("SELECT * FROM users")

	if err := AddOrderByClause(buf, "created_at", "user_id", "ASC", true); err != nil {
		t.Fatalf("err = %v", err)
	}

	result := buf.String()
	if !strings.Contains(result, `ORDER BY "created_at" DESC`) {
		t.Errorf("expected reversed direction, got: %s", result)
	}
}

func TestAddOrderByClause_SkipsSecondarySortWhenSameField(t *testing.T) {
	buf := &bytes.Buffer{}
	buf.WriteString("SELECT * FROM users")

	if err := AddOrderByClause(buf, "user_id", "user_id", "ASC", false); err != nil {
		t.Fatalf("err = %v", err)
	}

	result := buf.String()
	count := strings.Count(result, `"user_id"`)
	if count != 1 {
		t.Errorf("expected 1 occurrence of user_id, got %d in: %s", count, result)
	}
}

func TestAddOrderByClause_RejectsInvalidField(t *testing.T) {
	buf := &bytes.Buffer{}
	err := AddOrderByClause(buf, "created_at; DROP TABLE", "user_id", "ASC", false)
	if err == nil {
		t.Fatal("err = nil, want error")
	}
}

// ---------------------------------------------------------------------------
// AddLimitClause
// ---------------------------------------------------------------------------

func TestAddLimitClause(t *testing.T) {
	buf := &bytes.Buffer{}
	buf.WriteString("SELECT * FROM users")
	data := make(pgx.NamedArgs)

	AddLimitClause(10, data, buf)

	if !strings.Contains(buf.String(), "LIMIT @limit") {
		t.Errorf("missing LIMIT clause in: %s", buf.String())
	}
	if data["limit"] != 10 {
		t.Errorf("limit = %v, want 10", data["limit"])
	}
}

func TestAddLimitClause_DefaultsForZero(t *testing.T) {
	buf := &bytes.Buffer{}
	data := make(pgx.NamedArgs)

	AddLimitClause(0, data, buf)

	if data["limit"] != 50 {
		t.Errorf("limit = %v, want 50", data["limit"])
	}
}

func TestAddLimitClause_DefaultsForNegative(t *testing.T) {
	buf := &bytes.Buffer{}
	data := make(pgx.NamedArgs)

	AddLimitClause(-5, data, buf)

	if data["limit"] != 50 {
		t.Errorf("limit = %v, want 50", data["limit"])
	}
}

// ---------------------------------------------------------------------------
// ApplyCursorPagination
// ---------------------------------------------------------------------------

func TestApplyCursorPagination_NilValues(t *testing.T) {
	buf := &bytes.Buffer{}
	buf.WriteString("SELECT * FROM users")
	data := make(pgx.NamedArgs)

	err := ApplyCursorPagination[string, string](buf, data, "created_at", "user_id", nil, nil, "ASC", false)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if buf.String() != "SELECT * FROM users" {
		t.Errorf("buffer modified: %s", buf.String())
	}
}

func TestApplyCursorPagination_AddsWHERE(t *testing.T) {
	buf := &bytes.Buffer{}
	buf.WriteString("SELECT * FROM users")
	data := make(pgx.NamedArgs)

	orderValue := "2024-01-01"
	keyValue := "abc123"

	err := ApplyCursorPagination(buf, data, "created_at", "user_id", &orderValue, &keyValue, "ASC", false)
	if err != nil {
		t.Fatalf("err = %v", err)
	}

	result := buf.String()
	if !strings.Contains(result, "WHERE") {
		t.Errorf("missing WHERE in: %s", result)
	}
	if !strings.Contains(result, `("created_at", "user_id") >`) {
		t.Errorf("missing tuple comparison in: %s", result)
	}
	if data["cursor_order_value"] != "2024-01-01" {
		t.Errorf("cursor_order_value = %v, want 2024-01-01", data["cursor_order_value"])
	}
	if data["cursor_pk"] != "abc123" {
		t.Errorf("cursor_pk = %v, want abc123", data["cursor_pk"])
	}
}

func TestApplyCursorPagination_AddsAND(t *testing.T) {
	buf := &bytes.Buffer{}
	buf.WriteString("SELECT * FROM users WHERE active = true")
	data := make(pgx.NamedArgs)

	orderValue := "2024-01-01"
	keyValue := "abc123"

	err := ApplyCursorPagination(buf, data, "created_at", "user_id", &orderValue, &keyValue, "ASC", false)
	if err != nil {
		t.Fatalf("err = %v", err)
	}

	result := buf.String()
	if !strings.Contains(result, " AND ") {
		t.Errorf("missing AND in: %s", result)
	}
}

// ---------------------------------------------------------------------------
// AliasedOrderField
// ---------------------------------------------------------------------------

func TestAliasedOrderField(t *testing.T) {
	result := AliasedOrderField("created_at", "u")
	if result != "u.created_at" {
		t.Errorf("got %q, want %q", result, "u.created_at")
	}
}

// ---------------------------------------------------------------------------
// extractSQLOperation
// ---------------------------------------------------------------------------

func TestExtractSQLOperation(t *testing.T) {
	tests := []struct {
		sql  string
		want string
	}{
		{"SELECT * FROM users", "SELECT"},
		{"INSERT INTO users VALUES (1)", "INSERT"},
		{"UPDATE users SET name = 'x'", "UPDATE"},
		{"DELETE FROM users WHERE id = 1", "DELETE"},
		{"WITH cte AS (SELECT 1) SELECT * FROM cte", "SELECT"},
		{"WITH cte AS (SELECT 1) INSERT INTO t SELECT * FROM cte", "SELECT"},
		{"  SELECT 1  ", "SELECT"},
		{"", "QUERY"},
		{"TRUNCATE TABLE users", "TRUNCATE"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := extractSQLOperation(tt.sql)
			if got != tt.want {
				t.Errorf("extractSQLOperation(%q) = %q, want %q", tt.sql, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// PrettyPrintSQL
// ---------------------------------------------------------------------------

func TestPrettyPrintSQL(t *testing.T) {
	input := "SELECT *\n\tFROM users\n\tWHERE id = 1"
	result := PrettyPrintSQL(input)

	if strings.Contains(result, "\n") {
		t.Errorf("result contains newlines: %q", result)
	}
	if strings.Contains(result, "\t") {
		t.Errorf("result contains tabs: %q", result)
	}
	if strings.Contains(result, "  ") {
		t.Errorf("result contains multiple spaces: %q", result)
	}
}

// ---------------------------------------------------------------------------
// ApplyCursorPaginationFromToken (via fop.DecodeCursor)
// ---------------------------------------------------------------------------

func TestApplyCursorPaginationFromToken_EmptyCursor(t *testing.T) {
	buf := &bytes.Buffer{}
	buf.WriteString("SELECT * FROM users")
	data := make(pgx.NamedArgs)

	config := CursorConfig{
		Cursor:     "",
		OrderField: "created_at",
		PKField:    "user_id",
		Direction:  "ASC",
	}

	err := ApplyCursorPaginationFromToken(buf, data, config, false)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if buf.String() != "SELECT * FROM users" {
		t.Errorf("buffer modified for empty cursor: %s", buf.String())
	}
}

func TestApplyCursorPaginationFromToken_ValidCursor(t *testing.T) {
	buf := &bytes.Buffer{}
	buf.WriteString("SELECT * FROM users")
	data := make(pgx.NamedArgs)

	// Build a valid fop.Cursor token.
	cursor := struct {
		OrderField string `json:"order_field"`
		OrderValue string `json:"order_value"`
		PK         string `json:"pk"`
	}{
		OrderField: "created_at",
		OrderValue: "2024-01-01T00:00:00Z",
		PK:         "user123",
	}
	jsonData, _ := json.Marshal(cursor)
	token := base64.URLEncoding.EncodeToString(jsonData)

	config := CursorConfig{
		Cursor:     token,
		OrderField: "created_at",
		PKField:    "user_id",
		Direction:  "ASC",
	}

	err := ApplyCursorPaginationFromToken(buf, data, config, false)
	if err != nil {
		t.Fatalf("err = %v", err)
	}

	result := buf.String()
	if !strings.Contains(result, "WHERE") {
		t.Errorf("missing WHERE in: %s", result)
	}
	if !strings.Contains(result, `"created_at"`) {
		t.Errorf("missing order field in: %s", result)
	}
}

func TestApplyCursorPaginationFromToken_StaleCursor(t *testing.T) {
	buf := &bytes.Buffer{}
	buf.WriteString("SELECT * FROM users")
	data := make(pgx.NamedArgs)

	// Cursor encoded with order_field "email", but config expects "created_at".
	cursor := struct {
		OrderField string `json:"order_field"`
		OrderValue string `json:"order_value"`
		PK         string `json:"pk"`
	}{
		OrderField: "email",
		OrderValue: "alice@example.com",
		PK:         "user_1",
	}
	jsonData, _ := json.Marshal(cursor)
	token := base64.URLEncoding.EncodeToString(jsonData)

	config := CursorConfig{
		Cursor:     token,
		OrderField: "created_at",
		PKField:    "user_id",
		Direction:  "ASC",
	}

	err := ApplyCursorPaginationFromToken(buf, data, config, false)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	// Stale cursor should be ignored — buffer unchanged.
	if buf.String() != "SELECT * FROM users" {
		t.Errorf("stale cursor modified buffer: %s", buf.String())
	}
}

// ===========================================================================
// SECURITY TESTS
// ===========================================================================

func TestSecurity_QuoteIdentifier_EmptyFromMapLookup(t *testing.T) {
	_, err := QuoteIdentifier("")
	if err == nil {
		t.Fatal("empty string from failed map lookup must be rejected")
	}
}

func TestSecurity_QuoteIdentifier_UnicodeInjection(t *testing.T) {
	attacks := []struct {
		name  string
		input string
	}{
		{"zero-width space", "user\u200Bid"},
		{"zero-width joiner", "user\u200Did"},
		{"fullwidth chars", "\uff55\uff53\uff45\uff52_id"},
		{"null byte", "user\x00id"},
		{"backspace", "user\x08id"},
		{"unicode quotes", "user\u2018id"},
		{"right-to-left override", "user\u202Eid"},
	}

	for _, tt := range attacks {
		t.Run(tt.name, func(t *testing.T) {
			_, err := QuoteIdentifier(tt.input)
			if err == nil {
				t.Errorf("QuoteIdentifier(%q) should reject unicode attack", tt.input)
			}
		})
	}
}

func TestSecurity_QuoteIdentifier_CommonPayloads(t *testing.T) {
	payloads := []struct {
		name  string
		input string
	}{
		{"union select", "id UNION SELECT"},
		{"comment injection", "id--"},
		{"comment block", "id/**/"},
		{"hex encoding", "0x75736572"},
		{"stacked query", "id;SELECT"},
		{"or true", "id OR 1=1"},
		{"and false", "id AND 1=0"},
		{"sleep injection", "id;SLEEP(5)"},
		{"benchmark", "id;BENCHMARK(1000000,SHA1('test'))"},
		{"into outfile", "id INTO OUTFILE"},
		{"load_file", "id;LOAD_FILE('/etc/passwd')"},
	}

	for _, tt := range payloads {
		t.Run(tt.name, func(t *testing.T) {
			_, err := QuoteIdentifier(tt.input)
			if err == nil {
				t.Errorf("QuoteIdentifier(%q) should reject payload", tt.input)
			}
		})
	}
}

func TestSecurity_AddOrderByClause_InjectionAttempts(t *testing.T) {
	attacks := []struct {
		name       string
		orderField string
		pkField    string
	}{
		{"order field injection", "created_at; DROP TABLE users", "user_id"},
		{"pk field injection", "created_at", "user_id; DROP TABLE users"},
		{"both fields injection", "a; DROP TABLE", "b; DROP TABLE"},
		{"empty order field", "", "user_id"},
		{"empty pk field", "created_at", ""},
	}

	for _, tt := range attacks {
		t.Run(tt.name, func(t *testing.T) {
			buf := &bytes.Buffer{}
			buf.WriteString("SELECT * FROM users")

			err := AddOrderByClause(buf, tt.orderField, tt.pkField, "ASC", false)
			if err == nil {
				t.Error("should reject injection attempt")
			}
		})
	}
}

func TestSecurity_ApplyCursorPagination_FieldInjection(t *testing.T) {
	attacks := []struct {
		name       string
		orderField string
		pkField    string
	}{
		{"malicious order field", "; DROP TABLE users--", "user_id"},
		{"malicious pk field", "created_at", "; DROP TABLE users--"},
		{"empty fields", "", ""},
	}

	for _, tt := range attacks {
		t.Run(tt.name, func(t *testing.T) {
			buf := &bytes.Buffer{}
			buf.WriteString("SELECT * FROM users")
			data := make(pgx.NamedArgs)

			orderValue := "2024-01-01"
			keyValue := "abc123"

			err := ApplyCursorPagination(buf, data, tt.orderField, tt.pkField, &orderValue, &keyValue, "ASC", false)
			if err == nil {
				t.Error("should reject field injection")
			}
		})
	}
}
