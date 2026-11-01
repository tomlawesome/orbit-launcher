package live

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

// The update leg's data sentinels (#324): household data put into the
// earlier release's database and storage before the update, and read back
// after it, the standard upgrade test of seeding N-1, upgrading and checking
// on N. Kept free of the live build tag, like update_evidence.go, so
// `go test ./...` checks the SQL and the comparisons on every run; the
// docker calls that use them are in update_test.go.
//
// The sentinel shape is Orbit's scripts/test-backup-restore.sh
// (insert_document_fixture, assert_fixture_present): a document row with a
// plain display_name, and a stored file whose bytes are known, written into
// the app container at the path the row names. The ciphertext is a plain
// string: nothing here decrypts it, and the update does not read it.
const (
	sentinelHouseholdID = "24324324-3240-4324-8324-000000000001"
	sentinelSectionID   = "24324324-3240-4324-8324-000000000002"
	sentinelItemID      = "24324324-3240-4324-8324-000000000003"
	sentinelDocumentID  = "24324324-3240-4324-8324-000000000004"
	// sentinelStorageKey is 64 hex characters, which the document store
	// requires of a key (STORAGE_KEY_PATTERN in Orbit's documents/storage.ts).
	sentinelStorageKey = "3243243243243243243243243243243243243243243243243243243243243243"
	// sentinelItemName is the item's title. Orbit keeps it in title_enc,
	// the only title column, so this is the stored value read back as it
	// is; the app would not decrypt it, and the leg never asks it to.
	sentinelItemName     = "launcher-update-sentinel-item"
	sentinelDocumentName = "launcher-update-sentinel.bin"
	sentinelFileContent  = "orbit-launcher-update-sentinel-00001"
)

// sentinelFilePath is where the document store keeps sentinelStorageKey
// (objects/<first two>/<next two>/<key>.bin under DOCUMENTS_ROOT, whose
// default is /var/lib/orbit/documents).
func sentinelFilePath() string {
	k := sentinelStorageKey
	return "/var/lib/orbit/documents/objects/" + k[:2] + "/" + k[2:4] + "/" + k + ".bin"
}

// sentinelFileSHA256 is the sha256 of the bytes written to sentinelFilePath.
func sentinelFileSHA256() string {
	sum := sha256.Sum256([]byte(sentinelFileContent))
	return hex.EncodeToString(sum[:])
}

// updateSentinels is what the leg records before the update and reads back
// after it; "" in a read-back means the row or file was not found.
type updateSentinels struct {
	ItemName     string // items.title_enc
	DocumentName string // documents.display_name
	FileSHA256   string // sha256 of the stored file
}

func wantedSentinels() updateSentinels {
	return updateSentinels{
		ItemName:     sentinelItemName,
		DocumentName: sentinelDocumentName,
		FileSHA256:   sentinelFileSHA256(),
	}
}

// updateSentinelSeedSQL is the one place that knows the EARLIER release's
// schema (#324). It is written against Orbit's drizzle/0000_baseline.sql,
// which every preview since 2026-10-16 carries unchanged: only the columns
// each table requires are set. When the earlier release moves on (the
// previous preview's schema drops or renames one of these columns, or adds a
// required one), this is the only thing to update. What is read back after
// the update goes through the current release's columns,
// sentinelReadBackSQL.
func updateSentinelSeedSQL() string {
	size := len(sentinelFileContent)
	return fmt.Sprintf(`
insert into households (id, name) values ('%[1]s', 'launcher-update-sentinel-household');
insert into sections (id, household_id, slug, name) values ('%[2]s', '%[1]s', 'launcher-update', 'Launcher update');
insert into items (id, household_id, section_id, currency, title_enc) values ('%[3]s', '%[1]s', '%[2]s', 'GBP', '%[4]s');
insert into documents (id, household_id, display_name, media_type, size_bytes, content_sha256, lifecycle, scan_status, available_at)
  values ('%[5]s', '%[1]s', '%[6]s', 'application/octet-stream', %[7]d, '%[8]s', 'available', 'skipped', now());
insert into document_crypto (document_id, storage_key, ciphertext_size, envelope_version, content_iv, content_auth_tag, wrapped_dek, wrap_iv, wrap_auth_tag, key_id)
  values ('%[5]s', '%[9]s', %[7]d, 1, 'bGF1bmNoZXItaXY', 'bGF1bmNoZXItdGFn', 'bGF1bmNoZXItZGVr', 'bGF1bmNoZXItd2l2', 'bGF1bmNoZXItd3Rn', 'launcher-update-key');
`, sentinelHouseholdID, sentinelSectionID, sentinelItemID, sentinelItemName,
		sentinelDocumentID, sentinelDocumentName, size, sentinelFileSHA256(), sentinelStorageKey)
}

// sentinelReadBackSQL reads one value back, by the current release's names.
func sentinelReadBackSQL(table, column, id string) string {
	return fmt.Sprintf("select %s from %s where id = '%s';", column, table, id)
}

// sentinelChanges names each sentinel that is not what was seeded, so a
// failure says which part of the household's data the update lost or
// changed (#324).
func sentinelChanges(want, got updateSentinels) []string {
	var out []string
	check := func(what, w, g string) {
		switch {
		case g == w:
		case g == "":
			out = append(out, fmt.Sprintf("the %s sentinel is gone after the update (was %q)", what, w))
		default:
			out = append(out, fmt.Sprintf("the %s sentinel changed: was %q, now %q", what, w, g))
		}
	}
	check("item", want.ItemName, got.ItemName)
	check("document row", want.DocumentName, got.DocumentName)
	check("document file", want.FileSHA256, got.FileSHA256)
	return out
}

// manifestVersion returns the Orbit version an Orbit release manifest
// records (.version), which is the version its image reports as
// ORBIT_VERSION.
func manifestVersion(data []byte) (string, error) {
	var m struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return "", fmt.Errorf("release manifest is not JSON: %w", err)
	}
	if m.Version == "" {
		return "", fmt.Errorf("release manifest records no version")
	}
	return m.Version, nil
}

// healthStatus reads the word /api/health answers with ("ready", "maintenance"
// or "degraded"), whatever the HTTP status.
func healthStatus(body []byte) (string, error) {
	var h struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(body, &h); err != nil {
		return "", fmt.Errorf("the health answer is not JSON: %w", err)
	}
	if strings.TrimSpace(h.Status) == "" {
		return "", fmt.Errorf("the health answer has no status")
	}
	return h.Status, nil
}
