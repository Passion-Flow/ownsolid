package monitoring

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	scrapeTestWorkspace = "a943b5a7-c4cf-4e3b-a12a-4b933fa51ef1"
	scrapeTestNode      = "a1790234-5080-451f-997f-f4f4af42bb80"
)

func TestScrapePolicyRequiresUniqueCanonicalPrivateAssignments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "assignment.json")
	valid := fmt.Sprintf(`{"format_version":1,"workspace_id":%q,"allowed_networks":["10.0.0.0/8","fd00::/8"],"endpoints":{%q:"10.0.0.1:9100"}}`, scrapeTestWorkspace, scrapeTestNode)
	write := func(s string) {
		t.Helper()
		if e := os.WriteFile(path, []byte(s), 0600); e != nil {
			t.Fatal(e)
		}
	}
	write(valid)
	if endpoints, e := readScrapePolicy(path, scrapeTestWorkspace); e != nil || endpoints[scrapeTestNode] != "10.0.0.1:9100" {
		t.Fatalf("invalid protected assignment read: %v", e)
	}
	for _, endpoint := range []string{"10.0.0.1:0", "172.16.0.1:9100", "[::ffff:10.0.0.1]:9100", "1.1.1.1:443", "node.example.invalid:9100", "169.254.169.254:80"} {
		write(strings.Replace(valid, "10.0.0.1:9100", endpoint, 1))
		if _, e := readScrapePolicy(path, scrapeTestWorkspace); e == nil {
			t.Fatal("accepted invalid endpoint", endpoint)
		}
	}
	for _, invalid := range []string{
		strings.Replace(valid, scrapeTestWorkspace, scrapeTestNode, 1),
		strings.Replace(valid, `"10.0.0.0/8"`, `"0.0.0.0/0"`, 1),
		strings.Replace(valid, "}}", `,"b1790234-5080-451f-997f-f4f4af42bb80":"10.0.0.1:9100"}}`, 1),
		strings.Replace(valid, "}}", `,"`+scrapeTestNode+`":"10.0.0.2:9100"}}`, 1),
		strings.Replace(valid, `"endpoints":`, `"site_id":"client","endpoints":`, 1),
	} {
		write(invalid)
		if _, e := readScrapePolicy(path, scrapeTestWorkspace); e == nil {
			t.Fatal("accepted malformed or foreign policy", invalid)
		}
	}
	write(valid)
	if e := os.Chmod(path, 0666); e != nil {
		t.Fatal(e)
	}
	if _, e := readScrapePolicy(path, scrapeTestWorkspace); e == nil {
		t.Fatal("accepted a publicly writable assignment")
	}
}

func TestScrapeInventorySerializesPublishersAndPreservesPriorFile(t *testing.T) {
	path, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	if e := os.Chmod(path, 0755); e != nil {
		t.Fatal(e)
	}
	if _, _, e := openScrapeDirectory(path); e == nil {
		t.Fatal("opened an unprotected publication directory")
	}
	if e := os.Chmod(path, 0700); e != nil {
		t.Fatal(e)
	}
	root, dir, e := openScrapeDirectory(path)
	if e != nil {
		t.Fatal(e)
	}
	defer root.Close()
	defer dir.Close()
	if _, _, e := openScrapeDirectory(path); e == nil || e.Error() != "MONITORING_SCRAPE_PUBLISH_BUSY" {
		t.Fatalf("concurrent publisher did not receive a retryable refusal: %v", e)
	}
	file := filepath.Join(path, "nodes-"+scrapeTestWorkspace+".json")
	if e := os.WriteFile(file, []byte("previous snapshot"), 0600); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if e := writeScrapeInventory(ctx, root, dir, scrapeTestWorkspace, []scrapeTarget{}); e == nil {
		t.Fatal("cancelled publisher replaced the snapshot")
	}
	if raw, e := os.ReadFile(file); e != nil || string(raw) != "previous snapshot" {
		t.Fatalf("failed publication mutated the previous snapshot: %q %v", raw, e)
	}
	if e := writeScrapeInventory(t.Context(), root, dir, scrapeTestWorkspace, []scrapeTarget{}); e != nil {
		t.Fatal(e)
	}
	if raw, e := os.ReadFile(file); e != nil || string(raw) != "[]\n" {
		t.Fatalf("withdrawal was not a valid empty discovery file: %q %v", raw, e)
	}
	if files, e := os.ReadDir(path); e != nil || len(files) != 1 {
		t.Fatalf("publisher left staging files: %v %v", files, e)
	}
}
