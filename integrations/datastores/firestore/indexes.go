package firestore

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	admin "cloud.google.com/go/firestore/apiv1/admin"
	"cloud.google.com/go/firestore/apiv1/admin/adminpb"
	"google.golang.org/api/iterator"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/gopernicus/gopernicus/sdk"
)

// The manifest's enum vocabulary, spelled exactly as the Firebase CLI writes it
// into firestore.indexes.json and as the Firestore Admin API names the same
// values. Schema reference:
// https://firebase.google.com/docs/reference/firestore/indexes/
const (
	// ScopeCollection indexes a collection under one specific parent document.
	ScopeCollection = "COLLECTION"
	// ScopeCollectionGroup indexes every collection with that id, at any depth.
	ScopeCollectionGroup = "COLLECTION_GROUP"

	// OrderAscending and OrderDescending are the directional index modes.
	OrderAscending  = "ASCENDING"
	OrderDescending = "DESCENDING"

	// ArrayContains is the only array index mode Firestore defines.
	ArrayContains = "CONTAINS"
)

// minCompositeFields is the Admin API's floor for a composite index. A
// one-field entry is a SINGLE-FIELD index, which Firestore creates
// automatically and which belongs in fieldOverrides, not in indexes — the
// server rejects it with InvalidArgument, so the parser rejects it first.
const minCompositeFields = 2

// documentIDField is the implicit final field of every composite index. The
// server appends it when a manifest omits it, so the probe compares
// manifest-without-it against live-with-it (see compositeFields).
const documentIDField = "__name__"

// indexesFileMode / indexesDirMode are the permissions ExportIndexes creates a
// host's manifest and its parent directories with, matching ExportMigrations in
// the SQL connectors.
const (
	indexesFileMode = 0o644
	indexesDirMode  = 0o755
)

// tmpSuffix names the file ExportIndexes writes before renaming it over dst.
// Beside dst, so the rename stays within one filesystem and is atomic.
const tmpSuffix = ".tmp"

// ProbeTimeout bounds ProbeIndexes when the caller's context carries no
// deadline of its own. The probe runs at WIRING TIME, in front of a host's
// boot, and it talks to the Admin API — a service whose latency has nothing to
// do with the data path. Without a bound, an Admin API that hangs would hang
// the boot silently instead of failing it; 30s is generous for a ListIndexes
// call plus one GetField per declared field override, and a caller that wants a
// different budget passes a context with its own deadline, which is honored
// unchanged.
const ProbeTimeout = 30 * time.Second

// stateReady is the only Admin API index state that answers a query. CREATING
// and NEEDS_REPAIR both mean "this query will fail in production".
const stateReady = "READY"

var (
	// ErrProbeUnavailableOnEmulator reports ProbeIndexes called against an
	// emulator client. The emulator has no index registry and enforces no
	// composite index, so there is nothing to probe and a nil answer would be a
	// false green — the whole reason the probe exists is that an emulator-green
	// suite says nothing about production's FAILED_PRECONDITION. A store
	// constructor running against the emulator passes WithoutIndexProbe(), the
	// explicit opt-out; the connector never skips silently.
	ErrProbeUnavailableOnEmulator = fmt.Errorf("firestore: ProbeIndexes is meaningless against the emulator — it keeps no index registry; pass WithoutIndexProbe(): %w", sdk.ErrInvalidInput)

	// ErrConflictingFieldOverride reports two manifests that define the SAME
	// (collectionGroup, fieldPath) field override with different single-field
	// index sets. Merging them would silently pick a winner, and the loser is
	// somebody's query, so the merge refuses and names both definitions.
	ErrConflictingFieldOverride = fmt.Errorf("firestore: conflicting field override definitions: %w", sdk.ErrInvalidInput)
)

// defaultFieldIndexes is the single-field configuration Firestore applies to a
// field that carries no explicit override: ascending, descending, and
// array-contains, all at collection scope. Collection-GROUP scope single-field
// indexes are NOT part of it — they must be requested by an override — which is
// what makes an absent Field resource a meaningful probe answer rather than an
// automatic pass.
var defaultFieldIndexes = []FieldOverrideIndex{
	{Order: OrderAscending, QueryScope: ScopeCollection},
	{Order: OrderDescending, QueryScope: ScopeCollection},
	{ArrayConfig: ArrayContains, QueryScope: ScopeCollection},
}

// IndexManifest is the firestore.indexes.json document: the composite indexes a
// database must have and the single-field configuration overrides it must
// carry. It mirrors the schema the Firebase CLI reads and writes
// (https://firebase.google.com/docs/reference/firestore/indexes/), so a store's
// embedded fragment, a host's checked-in manifest, and `firebase deploy --only
// firestore:indexes` all speak one format.
//
// It is the migration analogue for a datastore with no DDL: a store embeds its
// fragment, a host merges it into its own manifest with ExportIndexes and
// deploys it, and the store's constructor proves the deployment with
// ProbeIndexes at wiring time instead of discovering it as a
// FAILED_PRECONDITION on a production request.
type IndexManifest struct {
	// Indexes are composite (multi-field) indexes. Field ORDER within each one
	// is semantic and is never rearranged.
	Indexes []CompositeIndex `json:"indexes"`

	// FieldOverrides replace the automatic single-field indexing of one field —
	// usually to disable it (an empty Indexes) or to add a collection-group
	// scoped single-field index the default configuration does not create.
	FieldOverrides []FieldOverride `json:"fieldOverrides,omitempty"`
}

// CompositeIndex is one entry of the manifest's "indexes" array.
type CompositeIndex struct {
	// CollectionGroup is the collection id the index serves. Required.
	CollectionGroup string `json:"collectionGroup"`

	// QueryScope is ScopeCollection or ScopeCollectionGroup. Required.
	QueryScope string `json:"queryScope"`

	// Fields are the indexed fields IN QUERY ORDER: equality filters first,
	// then the range/order field, then the tiebreak. At least
	// minCompositeFields of them. The trailing __name__ entry Firestore adds
	// itself may be stated or omitted.
	Fields []IndexField `json:"fields"`
}

// IndexField is one field of a composite index. Exactly one of Order and
// ArrayConfig is set — a field is either directional or an array membership
// index, never both and never neither.
type IndexField struct {
	FieldPath   string `json:"fieldPath"`
	Order       string `json:"order,omitempty"`
	ArrayConfig string `json:"arrayConfig,omitempty"`
}

// FieldOverride is one entry of the manifest's "fieldOverrides" array: the
// explicit single-field index configuration for one field.
type FieldOverride struct {
	CollectionGroup string `json:"collectionGroup"`
	FieldPath       string `json:"fieldPath"`

	// TTL is the Firebase CLI's per-field time-to-live flag. It is carried
	// through parse, merge, and export unchanged so a host manifest that sets
	// it survives a round trip; the connector never interprets it and the probe
	// never checks it. Absent (nil) is not the same as false, which is why it
	// is a pointer.
	TTL *bool `json:"ttl,omitempty"`

	// Indexes is the single-field index set this field must have. An EMPTY
	// (but present) array means "no single-field indexes", which is a real and
	// deliberate configuration, so it is always serialized as [] and never
	// omitted.
	Indexes []FieldOverrideIndex `json:"indexes"`
}

// FieldOverrideIndex is one single-field index inside a FieldOverride. Exactly
// one of Order and ArrayConfig is set, and QueryScope is required.
type FieldOverrideIndex struct {
	Order       string `json:"order,omitempty"`
	ArrayConfig string `json:"arrayConfig,omitempty"`
	QueryScope  string `json:"queryScope"`
}

// liveIndex is the connector's reduction of an Admin API composite index: the
// identity a manifest entry can be compared against, plus the serving state. It
// exists so the comparison is a pure function over a slice, testable without a
// GCP project (the Admin API is the one part of this connector no emulator can
// stand in for).
type liveIndex struct {
	CollectionGroup string
	QueryScope      string
	Fields          []IndexField
	State           string
}

// ParseIndexManifest reads and validates the firestore.indexes.json document at
// name within fsys — normally a store's //go:embed FS.
//
// Parsing is STRICT: unknown JSON keys are rejected rather than dropped, so a
// misspelled "queryscope" fails at wiring time instead of shipping an index
// nobody deployed. Validation requires a non-empty collectionGroup, a known
// queryScope, at least minCompositeFields fields per composite index, exactly
// one of order/arrayConfig per field with a known value, and no two composite
// entries (or two field overrides) with the same identity.
//
// I/O failures are returned as themselves (fs.ErrNotExist and friends);
// everything the parser judges wraps sdk.ErrInvalidInput.
func ParseIndexManifest(fsys fs.FS, name string) (IndexManifest, error) {
	data, err := fs.ReadFile(fsys, name)
	if err != nil {
		return IndexManifest{}, fmt.Errorf("firestore: reading index manifest %s: %w", name, err)
	}
	return parseIndexManifest(data, name)
}

// parseIndexManifest is the byte-level parser both ParseIndexManifest and
// ExportIndexes (which reads an OS path) share.
func parseIndexManifest(data []byte, source string) (IndexManifest, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()

	var m IndexManifest
	if err := dec.Decode(&m); err != nil {
		return IndexManifest{}, fmt.Errorf("firestore: parsing index manifest %s: %v: %w", source, err, sdk.ErrInvalidInput)
	}
	if dec.More() {
		return IndexManifest{}, fmt.Errorf("firestore: parsing index manifest %s: trailing content after the JSON document: %w", source, sdk.ErrInvalidInput)
	}
	if err := m.validate(source); err != nil {
		return IndexManifest{}, err
	}
	return m, nil
}

// Merge unions this manifest with other and returns the result in the canonical
// order (see sorted): composite indexes ordered by collectionGroup, then
// queryScope, then field by field (path, then mode), then by field count; field
// overrides by collectionGroup then fieldPath, each with its single-field index
// set ordered by queryScope, then order, then arrayConfig.
//
// Composite indexes union by IDENTITY — (collectionGroup, queryScope, the
// ordered fields) — so an entry present in both sides collapses to one and no
// entry of either side is ever dropped. A composite index has no attributes
// beyond its identity, so composites cannot conflict.
//
// Field overrides union by (collectionGroup, fieldPath). Two definitions of the
// same field with the same single-field index SET collapse (set equality, not
// list order); differing sets are a conflict and fail with
// ErrConflictingFieldOverride naming both definitions, because merging them
// would quietly break whichever query lost. A TTL flag set on one side only is
// adopted; conflicting TTL values are a conflict too.
//
// Both inputs are validated first, so Merge cannot manufacture a manifest the
// parser would reject.
func (m IndexManifest) Merge(other IndexManifest) (IndexManifest, error) {
	if err := m.validate("manifest"); err != nil {
		return IndexManifest{}, err
	}
	if err := other.validate("merged manifest"); err != nil {
		return IndexManifest{}, err
	}

	out := IndexManifest{}

	seen := make(map[string]bool)
	for _, idx := range slices.Concat(m.Indexes, other.Indexes) {
		key := compositeKey(idx)
		if seen[key] {
			continue
		}
		seen[key] = true
		out.Indexes = append(out.Indexes, idx)
	}

	byField := make(map[string]int)
	for _, o := range slices.Concat(m.FieldOverrides, other.FieldOverrides) {
		key := overrideKey(o)
		at, ok := byField[key]
		if !ok {
			byField[key] = len(out.FieldOverrides)
			out.FieldOverrides = append(out.FieldOverrides, o)
			continue
		}
		merged, err := mergeOverride(out.FieldOverrides[at], o)
		if err != nil {
			return IndexManifest{}, err
		}
		out.FieldOverrides[at] = merged
	}

	return out.sorted(), nil
}

// ExportIndexes writes m into the host's manifest at dst — the scaffold step a
// store exposes, the index analogue of ExportMigrations.
//
// A migration stream was append-only files; a manifest is ONE shared document,
// so export MERGES rather than copies: an existing dst is parsed and unioned
// with m (Merge's rules), which is what keeps a host's unrelated indexes and
// field overrides intact. An absent dst is created, parent directories
// included.
//
// The output is byte-stable: canonical order, two-space indent, one trailing
// newline, no HTML escaping. Exporting the same manifest twice therefore
// produces identical bytes, and re-exporting after a store's fragment has not
// changed is an empty diff. Two things it refuses rather than does: writing a
// document the parser would reject (validation runs on the merged result), and
// silently resolving a field-override conflict — an existing dst that
// contradicts m fails with ErrConflictingFieldOverride and dst is left
// untouched.
func ExportIndexes(m IndexManifest, dst string) error {
	if err := m.validate("manifest"); err != nil {
		return err
	}

	out := m.sorted()
	existing, err := os.ReadFile(dst)
	switch {
	case err == nil:
		host, perr := parseIndexManifest(existing, dst)
		if perr != nil {
			return perr
		}
		merged, merr := host.Merge(m)
		if merr != nil {
			return fmt.Errorf("firestore: merging into the existing manifest %s: %w", dst, merr)
		}
		out = merged
	case errors.Is(err, fs.ErrNotExist):
		if dir := filepath.Dir(dst); dir != "" {
			if err := os.MkdirAll(dir, indexesDirMode); err != nil {
				return err
			}
		}
	default:
		return err
	}

	data, err := marshalIndexManifest(out)
	if err != nil {
		return err
	}
	// Belt and suspenders: never leave behind a manifest ParseIndexManifest
	// would refuse to read back.
	if _, err := parseIndexManifest(data, dst); err != nil {
		return err
	}

	// Write through a temporary file and rename it over dst. os.WriteFile
	// truncates first, so an interrupted or short write would leave the host's
	// checked-in manifest — the file this function just merged INTO — empty or
	// half-written. rename(2) is atomic within a directory, so a reader sees
	// either the old manifest or the new one. The temporary lives beside dst so
	// the rename cannot cross a filesystem boundary; a failure after it is
	// created removes it, so a refused export leaves no debris.
	tmp := dst + tmpSuffix
	if err := os.WriteFile(tmp, data, indexesFileMode); err != nil {
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// ProbeIndexes verifies that every composite index in m exists and is READY on
// the database db is bound to, and that every field override in m is reflected
// in that field's live single-field configuration. It is the boot check a store
// constructor runs so a missing index fails at WIRING TIME, naming the index,
// instead of failing on a host's first production query.
//
// It answers:
//
//   - nil — everything in the manifest is deployed and serving.
//   - ErrProbeUnavailableOnEmulator — db is an emulator client. Checked FIRST,
//     before any Admin API client exists, because the emulator environment is
//     honored only by the data client: an Admin call made from an emulator run
//     would either fail or, worse, reach real GCP with ambient credentials.
//   - a *MissingIndexError (one gap) or an errors.Join of them (several). Both
//     satisfy errors.Is(err, ErrMissingIndex) — hence sdk.ErrUnavailable — and
//     errors.As reaches the first gap.
//   - an sdk.ErrForbidden-wrapped error naming the datastore.indexes.list
//     permission when the credential cannot read the index registry. A host
//     whose runtime service account cannot be granted it passes
//     WithoutIndexProbe() and takes responsibility for deployment itself.
//
// An empty manifest requires nothing and issues no RPC.
//
// # A probe failure is a wiring error, never a retry
//
// ErrMissingIndex wraps sdk.ErrUnavailable because the query is not wrong and
// the database is not broken — the index simply is not there. That sentinel is
// the one a caller's generic infrastructure retry watches for, and retrying a
// missing index only delays the boot failure: no amount of waiting deploys an
// index. A caller that retries sdk.ErrUnavailable MUST branch first:
//
//	if err := firestoredb.ProbeIndexesFS(ctx, db, IndexesFS, IndexesFile); err != nil {
//	    if errors.Is(err, firestoredb.ErrMissingIndex) {
//	        return err // deploy the manifest; retrying cannot help
//	    }
//	    // only now is a transport failure worth another attempt
//	}
//
// The one exception proves the rule: an index reported as CREATING becomes
// READY on its own, which is why the CI live leg WAITS for the build to finish
// before it runs its query matrix — deliberately, in the provisioning step,
// not by looping a boot probe.
//
// # Deadline
//
// A caller's deadline is honored as given. A context without one is bounded by
// ProbeTimeout, so an Admin API that never answers fails the boot instead of
// hanging it.
//
// What is NOT probed, stated plainly because a probe that implies more than it
// checks is worse than none:
//
//   - A field override the manifest does NOT declare. The manifest is the
//     specification; if a store's query depends on a field's DEFAULT
//     single-field indexing surviving, it declares that dependency as a
//     fieldOverride entry, and only then is it checked. A host disabling
//     indexing on some other field is invisible here.
//   - A DECLARED field override whose index set is EMPTY. It names a field and
//     asks for nothing, so there is nothing to compare and no RPC is issued for
//     it (probeFieldOverride returns early). Such an entry documents intent —
//     usually a TTL setting the probe never interprets — and must not be read
//     as "this field's indexing was checked".
//   - TTL configuration, index density, and multikey/vector/search index modes.
//   - Whether an index is WIDE ENOUGH for a query the manifest never described.
//     A probe proves the manifest was deployed; the store's query matrix
//     proves the manifest is right, and only the live query leg proves that.
func ProbeIndexes(ctx context.Context, db *DB, m IndexManifest) error {
	if db == nil {
		return fmt.Errorf("firestore: ProbeIndexes needs an open DB: %w", sdk.ErrInvalidInput)
	}
	if db.Emulated() {
		return ErrProbeUnavailableOnEmulator
	}
	if err := m.validate("manifest"); err != nil {
		return err
	}
	if len(m.Indexes) == 0 && len(m.FieldOverrides) == 0 {
		return nil
	}

	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, ProbeTimeout)
		defer cancel()
	}

	client, err := admin.NewFirestoreAdminClient(ctx, db.clientOpts...)
	if err != nil {
		return fmt.Errorf("firestore: opening the admin client for %s: %w", db.Target(), MapError(err))
	}
	defer client.Close()

	var gaps []error

	if len(m.Indexes) > 0 {
		live, err := listLiveIndexes(ctx, client, db)
		if err != nil {
			return err
		}
		for _, idx := range missingComposites(m.Indexes, live) {
			gaps = append(gaps, db.missingIndexError(idx.message()))
		}
	}

	for _, o := range m.FieldOverrides {
		missing, err := probeFieldOverride(ctx, client, db, o)
		if err != nil {
			return err
		}
		for _, want := range missing {
			gaps = append(gaps, db.missingIndexError(fmt.Sprintf(
				"collection group %q field %q requires a single-field index (%s, scope %s) that its live configuration does not have",
				o.CollectionGroup, o.FieldPath, fieldMode(IndexField{Order: want.Order, ArrayConfig: want.ArrayConfig}), want.QueryScope,
			)))
		}
	}

	switch len(gaps) {
	case 0:
		return nil
	case 1:
		return gaps[0]
	default:
		return errors.Join(gaps...)
	}
}

// ProbeIndexesFS parses the manifest at name within fsys and probes it. It is
// the one-line form a store constructor uses, since a store already holds its
// manifest as an embedded FS:
//
//	if err := firestoredb.ProbeIndexesFS(ctx, db, IndexesFS, IndexesFile); err != nil { … }
func ProbeIndexesFS(ctx context.Context, db *DB, fsys fs.FS, name string) error {
	m, err := ParseIndexManifest(fsys, name)
	if err != nil {
		return err
	}
	return ProbeIndexes(ctx, db, m)
}

// missingIndexError builds a probe gap as the same *MissingIndexError a
// FAILED_PRECONDITION from a real query produces, so a host has ONE type and
// ONE sentinel to handle whether the diagnosis came from the boot probe or from
// production. URL is this database's console index page — constructed here, not
// server-supplied, because the probe has no server message to quote.
func (d *DB) missingIndexError(msg string) *MissingIndexError {
	return &MissingIndexError{Message: msg, URL: d.indexesConsoleURL()}
}

// indexesConsoleURL is the Cloud console page listing this database's indexes.
func (d *DB) indexesConsoleURL() string {
	return fmt.Sprintf("https://console.cloud.google.com/firestore/databases/%s/indexes?project=%s",
		url.PathEscape(d.database), url.QueryEscape(d.project))
}

// indexGap is one manifest composite index the live database cannot serve, with
// the state that was observed ("" when no index with that identity exists).
type indexGap struct {
	Index CompositeIndex
	State string
}

// message renders the gap for an operator: which collection group, which
// scope, which fields in which order, and what the database has instead.
func (g indexGap) message() string {
	fields := make([]string, 0, len(g.Index.Fields))
	for _, f := range g.Index.Fields {
		fields = append(fields, fmt.Sprintf("%s %s", f.FieldPath, fieldMode(f)))
	}
	joined := strings.Join(fields, ", ")
	if g.State == "" {
		return fmt.Sprintf("collection group %q has no composite index (scope %s) on [%s]",
			g.Index.CollectionGroup, g.Index.QueryScope, joined)
	}
	return fmt.Sprintf("collection group %q has a composite index (scope %s) on [%s] but its state is %s, not %s",
		g.Index.CollectionGroup, g.Index.QueryScope, joined, g.State, stateReady)
}

// missingComposites is the probe's whole comparison, as a pure function over
// the live index set so it can be tested without an Admin API.
//
// A manifest entry is satisfied by a live index with the same identity —
// collection group, query scope, and the ordered fields, each with the same
// mode — in state READY. Field ORDER is part of the identity (a composite index
// on (a, b) does not serve a query ordered by (b, a)) and so is the direction
// of each field. Live indexes the manifest never asked for are ignored: a host
// may have any number of its own.
//
// The trailing __name__ field is handled on the manifest's terms: the server
// appends it to every composite index, and a manifest that omits it is matched
// against the live index without it. A manifest that STATES it is matched
// including its direction, which is how a store pins a document-id tiebreak
// that differs from the last field's direction.
//
// A live index that exists but is CREATING or NEEDS_REPAIR is reported with
// that state rather than as absent, because "deploy it" and "wait for it" are
// different instructions to an operator.
func missingComposites(want []CompositeIndex, live []liveIndex) []indexGap {
	ready := make(map[string]bool)
	state := make(map[string]string)
	for _, l := range live {
		for _, key := range liveKeys(l) {
			if l.State == stateReady {
				ready[key] = true
			}
			if _, seen := state[key]; !seen || l.State == stateReady {
				state[key] = l.State
			}
		}
	}

	var gaps []indexGap
	for _, idx := range want {
		key := compositeKey(idx)
		if ready[key] {
			continue
		}
		gaps = append(gaps, indexGap{Index: idx, State: state[key]})
	}
	return gaps
}

// liveKeys returns the identity keys a live index answers to: its fields as
// reported, and — when the server appended the implicit __name__ tiebreak — the
// same index with that field removed, which is the shape a manifest usually
// spells. One live index therefore satisfies either spelling, and neither
// spelling matches an index whose real field tuple differs.
func liveKeys(l liveIndex) []string {
	keys := []string{compositeKey(CompositeIndex{CollectionGroup: l.CollectionGroup, QueryScope: l.QueryScope, Fields: l.Fields})}
	if n := len(l.Fields); n > 0 && l.Fields[n-1].FieldPath == documentIDField {
		keys = append(keys, compositeKey(CompositeIndex{
			CollectionGroup: l.CollectionGroup,
			QueryScope:      l.QueryScope,
			Fields:          l.Fields[:n-1],
		}))
	}
	return keys
}

// listLiveIndexes reads every composite index of the database through the Admin
// API. The parent wildcard "-" as the collection group is the documented way to
// ask for all of them at once
// (projects/{p}/databases/{d}/collectionGroups/-), so the probe costs ONE
// paginated call regardless of how many collections a manifest touches.
func listLiveIndexes(ctx context.Context, client *admin.FirestoreAdminClient, db *DB) ([]liveIndex, error) {
	it := client.ListIndexes(ctx, &adminpb.ListIndexesRequest{Parent: db.Target() + "/collectionGroups/-"})

	var out []liveIndex
	for {
		idx, err := it.Next()
		if errors.Is(err, iterator.Done) {
			return out, nil
		}
		if err != nil {
			return nil, db.adminError("listing the composite indexes", "datastore.indexes.list", err)
		}
		if idx.GetApiScope() != adminpb.Index_ANY_API {
			continue
		}
		out = append(out, liveIndex{
			CollectionGroup: collectionGroupOf(idx.GetName()),
			QueryScope:      idx.GetQueryScope().String(),
			Fields:          liveFields(idx.GetFields()),
			State:           idx.GetState().String(),
		})
	}
}

// probeFieldOverride reads one field's live configuration and returns the
// declared single-field indexes it does not provide.
//
// A field with no explicit override is reported by the Admin API as inheriting
// the ancestor configuration, and some databases answer NotFound for it
// outright; both are treated as "the default configuration", which supplies
// ascending, descending, and array-contains at COLLECTION scope and nothing
// else. A manifest that asks for exactly those is satisfied; one that asks for
// a collection-GROUP scoped single-field index is not, and says so.
func probeFieldOverride(ctx context.Context, client *admin.FirestoreAdminClient, db *DB, o FieldOverride) ([]FieldOverrideIndex, error) {
	if len(o.Indexes) == 0 {
		return nil, nil
	}

	name := fmt.Sprintf("%s/collectionGroups/%s/fields/%s", db.Target(), o.CollectionGroup, o.FieldPath)
	field, err := client.GetField(ctx, &adminpb.GetFieldRequest{Name: name})
	switch {
	case status.Code(err) == codes.NotFound:
		return missingFieldIndexes(o.Indexes, defaultFieldIndexes), nil
	case err != nil:
		// GetField (firestore.googleapis.com/…/collectionGroups.fields.get) is
		// authorized by datastore.indexes.list in the IAM permission tables —
		// NOT by a "fields.get" permission, which does not exist. Naming the
		// wrong permission in a denial message costs an operator an hour, so
		// this is the same permission the ListIndexes call above names, and it
		// is what roles/datastore.indexAdmin and roles/datastore.owner grant.
		return nil, db.adminError(fmt.Sprintf("reading the field configuration of %s", name), "datastore.indexes.list", err)
	}

	cfg := field.GetIndexConfig()
	if cfg == nil || cfg.GetUsesAncestorConfig() {
		return missingFieldIndexes(o.Indexes, defaultFieldIndexes), nil
	}
	return missingFieldIndexes(o.Indexes, liveFieldIndexes(cfg.GetIndexes())), nil
}

// missingFieldIndexes returns the entries of want that have is not present in
// have, comparing on (mode, queryScope). Pure, so the branch table above is
// testable without a project.
func missingFieldIndexes(want, have []FieldOverrideIndex) []FieldOverrideIndex {
	present := make(map[string]bool, len(have))
	for _, h := range have {
		present[overrideIndexKey(h)] = true
	}

	var missing []FieldOverrideIndex
	for _, w := range want {
		if !present[overrideIndexKey(w)] {
			missing = append(missing, w)
		}
	}
	return missing
}

// adminError maps an Admin API failure, giving a denied credential the one
// message that actually helps: the permission to grant, and the escape hatch
// for a host that cannot grant it.
func (d *DB) adminError(what, permission string, err error) error {
	if status.Code(err) == codes.PermissionDenied {
		return fmt.Errorf("firestore: %s of %s requires the %s permission (roles/datastore.indexAdmin or roles/datastore.viewer grant it); a host that cannot grant it must pass WithoutIndexProbe() and deploy the index manifest itself: %w",
			what, d.Target(), permission, MapError(err))
	}
	return fmt.Errorf("firestore: %s of %s: %w", what, d.Target(), MapError(err))
}

// liveFields converts Admin API index fields into manifest fields. A vector,
// search, or unspecified mode maps to a field with neither Order nor
// ArrayConfig, which no valid manifest entry can equal — such an index simply
// never matches instead of accidentally satisfying a directional requirement.
func liveFields(fields []*adminpb.Index_IndexField) []IndexField {
	out := make([]IndexField, 0, len(fields))
	for _, f := range fields {
		field := IndexField{FieldPath: f.GetFieldPath()}
		switch {
		case f.GetOrder() != adminpb.Index_IndexField_ORDER_UNSPECIFIED:
			field.Order = f.GetOrder().String()
		case f.GetArrayConfig() != adminpb.Index_IndexField_ARRAY_CONFIG_UNSPECIFIED:
			field.ArrayConfig = f.GetArrayConfig().String()
		}
		out = append(out, field)
	}
	return out
}

// liveFieldIndexes converts a Field's single-field index configuration into the
// manifest's vocabulary.
func liveFieldIndexes(indexes []*adminpb.Index) []FieldOverrideIndex {
	out := make([]FieldOverrideIndex, 0, len(indexes))
	for _, idx := range indexes {
		entry := FieldOverrideIndex{QueryScope: idx.GetQueryScope().String()}
		if fields := liveFields(idx.GetFields()); len(fields) == 1 {
			entry.Order = fields[0].Order
			entry.ArrayConfig = fields[0].ArrayConfig
		}
		out = append(out, entry)
	}
	return out
}

// collectionGroupOf extracts the collection id from an index resource name,
// projects/{p}/databases/{d}/collectionGroups/{c}/indexes/{id}.
func collectionGroupOf(name string) string {
	const marker = "/collectionGroups/"
	at := strings.Index(name, marker)
	if at < 0 {
		return ""
	}
	rest := name[at+len(marker):]
	if end := strings.Index(rest, "/"); end >= 0 {
		return rest[:end]
	}
	return rest
}

// validate reports every rule the manifest format imposes, wrapping
// sdk.ErrInvalidInput and naming the offending entry by index so a hand-edited
// host manifest can be fixed without guessing.
func (m IndexManifest) validate(source string) error {
	seen := make(map[string]int, len(m.Indexes))
	for i, idx := range m.Indexes {
		if err := idx.validate(); err != nil {
			return fmt.Errorf("firestore: index manifest %s: indexes[%d]: %v: %w", source, i, err, sdk.ErrInvalidInput)
		}
		key := compositeKey(idx)
		if first, dup := seen[key]; dup {
			return fmt.Errorf("firestore: index manifest %s: indexes[%d] duplicates indexes[%d] (collection group %q, scope %s, same fields): %w",
				source, i, first, idx.CollectionGroup, idx.QueryScope, sdk.ErrInvalidInput)
		}
		seen[key] = i
	}

	seenFields := make(map[string]int, len(m.FieldOverrides))
	for i, o := range m.FieldOverrides {
		if err := o.validate(); err != nil {
			return fmt.Errorf("firestore: index manifest %s: fieldOverrides[%d]: %v: %w", source, i, err, sdk.ErrInvalidInput)
		}
		key := overrideKey(o)
		if first, dup := seenFields[key]; dup {
			return fmt.Errorf("firestore: index manifest %s: fieldOverrides[%d] duplicates fieldOverrides[%d] (collection group %q, field %q): %w",
				source, i, first, o.CollectionGroup, o.FieldPath, sdk.ErrInvalidInput)
		}
		seenFields[key] = i
	}
	return nil
}

// validate checks one composite index entry.
func (c CompositeIndex) validate() error {
	if strings.TrimSpace(c.CollectionGroup) == "" {
		return errors.New("empty collectionGroup")
	}
	if err := validScope(c.QueryScope); err != nil {
		return err
	}
	if len(c.Fields) < minCompositeFields {
		return fmt.Errorf("a composite index needs at least %d fields, got %d (a single-field index belongs in fieldOverrides)", minCompositeFields, len(c.Fields))
	}
	for i, f := range c.Fields {
		if err := f.validate(); err != nil {
			return fmt.Errorf("fields[%d]: %w", i, err)
		}
	}
	return nil
}

// validate checks one composite index field.
func (f IndexField) validate() error {
	if strings.TrimSpace(f.FieldPath) == "" {
		return errors.New("empty fieldPath")
	}
	if strings.Contains(f.FieldPath, "/") {
		return fmt.Errorf("fieldPath %q contains a slash", f.FieldPath)
	}
	switch {
	case f.Order != "" && f.ArrayConfig != "":
		return fmt.Errorf("fieldPath %q sets both order and arrayConfig", f.FieldPath)
	case f.Order != "":
		return validOrder(f.Order)
	case f.ArrayConfig != "":
		return validArrayConfig(f.ArrayConfig)
	default:
		return fmt.Errorf("fieldPath %q sets neither order nor arrayConfig", f.FieldPath)
	}
}

// validate checks one field override entry.
func (o FieldOverride) validate() error {
	if strings.TrimSpace(o.CollectionGroup) == "" {
		return errors.New("empty collectionGroup")
	}
	if strings.TrimSpace(o.FieldPath) == "" {
		return errors.New("empty fieldPath")
	}
	if strings.Contains(o.FieldPath, "/") {
		return fmt.Errorf("fieldPath %q contains a slash", o.FieldPath)
	}
	seen := make(map[string]bool, len(o.Indexes))
	for i, idx := range o.Indexes {
		if err := idx.validate(); err != nil {
			return fmt.Errorf("indexes[%d]: %w", i, err)
		}
		key := overrideIndexKey(idx)
		if seen[key] {
			return fmt.Errorf("indexes[%d] is a duplicate", i)
		}
		seen[key] = true
	}
	return nil
}

// validate checks one single-field index inside a field override.
func (i FieldOverrideIndex) validate() error {
	if err := validScope(i.QueryScope); err != nil {
		return err
	}
	switch {
	case i.Order != "" && i.ArrayConfig != "":
		return errors.New("sets both order and arrayConfig")
	case i.Order != "":
		return validOrder(i.Order)
	case i.ArrayConfig != "":
		return validArrayConfig(i.ArrayConfig)
	default:
		return errors.New("sets neither order nor arrayConfig")
	}
}

func validScope(scope string) error {
	if scope != ScopeCollection && scope != ScopeCollectionGroup {
		return fmt.Errorf("queryScope %q is not %s or %s", scope, ScopeCollection, ScopeCollectionGroup)
	}
	return nil
}

func validOrder(order string) error {
	if order != OrderAscending && order != OrderDescending {
		return fmt.Errorf("order %q is not %s or %s", order, OrderAscending, OrderDescending)
	}
	return nil
}

func validArrayConfig(cfg string) error {
	if cfg != ArrayContains {
		return fmt.Errorf("arrayConfig %q is not %s", cfg, ArrayContains)
	}
	return nil
}

// mergeOverride unions two definitions of the same field override, refusing a
// disagreement. Set equality — not list order — decides, so a host that lists
// the same two single-field indexes in the other order is not a conflict.
func mergeOverride(a, b FieldOverride) (FieldOverride, error) {
	if !equalOverrideIndexSets(a.Indexes, b.Indexes) {
		return FieldOverride{}, fmt.Errorf("firestore: collection group %q field %q is defined as %s and as %s: %w",
			a.CollectionGroup, a.FieldPath, describeOverrideIndexes(a.Indexes), describeOverrideIndexes(b.Indexes), ErrConflictingFieldOverride)
	}
	if a.TTL != nil && b.TTL != nil && *a.TTL != *b.TTL {
		return FieldOverride{}, fmt.Errorf("firestore: collection group %q field %q sets ttl both %v and %v: %w",
			a.CollectionGroup, a.FieldPath, *a.TTL, *b.TTL, ErrConflictingFieldOverride)
	}
	out := a
	if out.TTL == nil {
		out.TTL = b.TTL
	}
	return out, nil
}

// equalOverrideIndexSets compares two single-field index sets ignoring order.
func equalOverrideIndexSets(a, b []FieldOverrideIndex) bool {
	if len(a) != len(b) {
		return false
	}
	seen := make(map[string]int, len(a))
	for _, i := range a {
		seen[overrideIndexKey(i)]++
	}
	for _, i := range b {
		key := overrideIndexKey(i)
		if seen[key] == 0 {
			return false
		}
		seen[key]--
	}
	return true
}

// describeOverrideIndexes renders a single-field index set for a conflict
// message, in canonical order so the two sides read comparably.
func describeOverrideIndexes(indexes []FieldOverrideIndex) string {
	if len(indexes) == 0 {
		return "[no single-field indexes]"
	}
	sorted := slices.Clone(indexes)
	slices.SortFunc(sorted, compareOverrideIndex)

	parts := make([]string, 0, len(sorted))
	for _, i := range sorted {
		parts = append(parts, fmt.Sprintf("%s scope %s", fieldMode(IndexField{Order: i.Order, ArrayConfig: i.ArrayConfig}), i.QueryScope))
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// sorted returns the manifest in the canonical order Merge and ExportIndexes
// promise, with nil slices normalized so the JSON carries "indexes": [] rather
// than a null. The order is documented on Merge; it exists so a re-export is a
// byte-identical no-op diff.
func (m IndexManifest) sorted() IndexManifest {
	out := IndexManifest{Indexes: slices.Clone(m.Indexes)}
	if out.Indexes == nil {
		out.Indexes = []CompositeIndex{}
	}
	slices.SortFunc(out.Indexes, compareComposite)

	if len(m.FieldOverrides) > 0 {
		out.FieldOverrides = make([]FieldOverride, 0, len(m.FieldOverrides))
		for _, o := range m.FieldOverrides {
			entry := o
			entry.Indexes = slices.Clone(o.Indexes)
			if entry.Indexes == nil {
				entry.Indexes = []FieldOverrideIndex{}
			}
			slices.SortFunc(entry.Indexes, compareOverrideIndex)
			out.FieldOverrides = append(out.FieldOverrides, entry)
		}
		slices.SortFunc(out.FieldOverrides, compareOverride)
	}
	return out
}

// marshalIndexManifest renders the manifest as the Firebase CLI's file shape:
// two-space indent, one trailing newline, and no HTML escaping (a field path
// with an ampersand must survive a round trip as itself).
func marshalIndexManifest(m IndexManifest) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(m); err != nil {
		return nil, fmt.Errorf("firestore: encoding the index manifest: %w", err)
	}
	return buf.Bytes(), nil
}

func compareComposite(a, b CompositeIndex) int {
	if c := cmp.Compare(a.CollectionGroup, b.CollectionGroup); c != 0 {
		return c
	}
	if c := cmp.Compare(a.QueryScope, b.QueryScope); c != 0 {
		return c
	}
	for i := 0; i < len(a.Fields) && i < len(b.Fields); i++ {
		if c := cmp.Compare(a.Fields[i].FieldPath, b.Fields[i].FieldPath); c != 0 {
			return c
		}
		if c := cmp.Compare(fieldMode(a.Fields[i]), fieldMode(b.Fields[i])); c != 0 {
			return c
		}
	}
	return cmp.Compare(len(a.Fields), len(b.Fields))
}

func compareOverride(a, b FieldOverride) int {
	if c := cmp.Compare(a.CollectionGroup, b.CollectionGroup); c != 0 {
		return c
	}
	return cmp.Compare(a.FieldPath, b.FieldPath)
}

func compareOverrideIndex(a, b FieldOverrideIndex) int {
	if c := cmp.Compare(a.QueryScope, b.QueryScope); c != 0 {
		return c
	}
	if c := cmp.Compare(a.Order, b.Order); c != 0 {
		return c
	}
	return cmp.Compare(a.ArrayConfig, b.ArrayConfig)
}

// compositeKey is a composite index's identity: collection group, query scope,
// and the ORDERED fields with their modes. Every union, duplicate check, and
// probe comparison goes through it, so they cannot disagree about what "the
// same index" means. The separators are control characters no manifest value
// may contain (fieldPath is validated slash-free and the enums are closed
// sets), so distinct tuples cannot collide.
func compositeKey(c CompositeIndex) string {
	var b strings.Builder
	b.WriteString(c.CollectionGroup)
	b.WriteByte(0x1f)
	b.WriteString(c.QueryScope)
	for _, f := range c.Fields {
		b.WriteByte(0x1e)
		b.WriteString(f.FieldPath)
		b.WriteByte(0x1f)
		b.WriteString(fieldMode(f))
	}
	return b.String()
}

// overrideKey is a field override's identity.
func overrideKey(o FieldOverride) string {
	return o.CollectionGroup + "\x1f" + o.FieldPath
}

// overrideIndexKey is one single-field index's identity within an override.
func overrideIndexKey(i FieldOverrideIndex) string {
	return fieldMode(IndexField{Order: i.Order, ArrayConfig: i.ArrayConfig}) + "\x1f" + i.QueryScope
}

// fieldMode renders how a field is indexed, as one comparable token.
func fieldMode(f IndexField) string {
	switch {
	case f.Order != "":
		return "order=" + f.Order
	case f.ArrayConfig != "":
		return "arrayConfig=" + f.ArrayConfig
	default:
		return "unindexed"
	}
}
