package readonlybash

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestApprovalCreateClaimPermissionsAndCanonicalCwd(t *testing.T) {
	root := t.TempDir()
	cwd := filepath.Join(root, "work")
	if err := os.Mkdir(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(cwd, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	approvalDir := filepath.Join(root, "approvals")
	wantCwd, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		t.Fatal(err)
	}

	approval, err := CreateApproval(CreateApprovalOptions{
		ApprovalDir:     approvalDir,
		RequestID:       "req-1",
		Cwd:             link,
		OriginalCommand: "pwd",
		CommandToRun:    "pwd",
	})
	if err != nil {
		t.Fatal(err)
	}
	if approval.CanonicalCwd != wantCwd {
		t.Fatalf("canonical cwd=%q want %q", approval.CanonicalCwd, wantCwd)
	}
	assertMode(t, approvalDir, 0o700)
	assertMode(t, filepath.Join(approvalDir, approval.ID+".json"), 0o600)

	if _, err := CreateApproval(CreateApprovalOptions{ApprovalDir: approvalDir, RequestID: "req-2", Cwd: cwd, OriginalCommand: "pwd", CommandToRun: "pwd"}); !errors.Is(err, ErrApprovalPending) {
		t.Fatalf("second approval err=%v want ErrApprovalPending", err)
	}
	if _, err := ClaimApproval(ClaimApprovalOptions{ApprovalDir: approvalDir, Cwd: root}); !errors.Is(err, ErrNoApproval) {
		t.Fatalf("wrong cwd claim err=%v want ErrNoApproval", err)
	}
	claimed, err := ClaimApproval(ClaimApprovalOptions{ApprovalDir: approvalDir, Cwd: cwd})
	if err != nil {
		t.Fatal(err)
	}
	if claimed.ID != approval.ID || claimed.RequestID != "req-1" {
		t.Fatalf("claimed wrong approval: %+v", claimed)
	}
	if _, err := os.Stat(filepath.Join(approvalDir, approval.ID+".json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("approval file still exists: %v", err)
	}
}

func TestApprovalStaleCleanupAndSingleFlight(t *testing.T) {
	root := t.TempDir()
	approvalDir := filepath.Join(root, "approvals")
	_, err := CreateApproval(CreateApprovalOptions{ApprovalDir: approvalDir, RequestID: "old", Cwd: root, OriginalCommand: "pwd", CommandToRun: "pwd", TTL: time.Nanosecond})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Millisecond)
	if _, err := CreateApproval(CreateApprovalOptions{ApprovalDir: approvalDir, RequestID: "new", Cwd: root, OriginalCommand: "pwd", CommandToRun: "pwd"}); err != nil {
		t.Fatalf("stale approval was not cleaned: %v", err)
	}
}

func TestApprovalConcurrentCreateSingleFlight(t *testing.T) {
	root := t.TempDir()
	approvalDir := filepath.Join(root, "approvals")
	var wg sync.WaitGroup
	results := make(chan error, 8)
	for i := 0; i < cap(results); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := CreateApproval(CreateApprovalOptions{ApprovalDir: approvalDir, RequestID: "req", Cwd: root, OriginalCommand: "pwd", CommandToRun: "pwd"})
			results <- err
		}()
	}
	wg.Wait()
	close(results)

	successes := 0
	pending := 0
	for err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrApprovalPending):
			pending++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if successes != 1 || pending != 7 {
		t.Fatalf("successes=%d pending=%d", successes, pending)
	}
}

func TestApprovalCrossProcessSingleFlight(t *testing.T) {
	root := t.TempDir()
	approvalDir := filepath.Join(root, "approvals")
	const workers = 4
	results := make(chan string, workers)
	for i := 0; i < workers; i++ {
		go func(i int) {
			cmd := exec.Command(os.Args[0], "-test.run=TestApprovalProcessHelper", "--", approvalDir, root, fmt.Sprintf("req-%d", i))
			cmd.Env = append(os.Environ(), "READONLY_BASH_APPROVAL_HELPER=1")
			out, err := cmd.CombinedOutput()
			if err != nil {
				results <- "error:" + err.Error() + ":" + string(out)
				return
			}
			results <- strings.TrimSpace(string(out))
		}(i)
	}
	ok, pending := 0, 0
	for i := 0; i < workers; i++ {
		switch result := <-results; result {
		case "ok":
			ok++
		case "pending":
			pending++
		default:
			t.Fatalf("unexpected child result: %s", result)
		}
	}
	if ok != 1 || pending != workers-1 {
		t.Fatalf("ok=%d pending=%d", ok, pending)
	}
}

func TestApprovalProcessHelper(t *testing.T) {
	if os.Getenv("READONLY_BASH_APPROVAL_HELPER") != "1" {
		return
	}
	for i, arg := range os.Args {
		if arg != "--" || i+3 >= len(os.Args) {
			continue
		}
		_, err := CreateApproval(CreateApprovalOptions{
			ApprovalDir:     os.Args[i+1],
			Cwd:             os.Args[i+2],
			RequestID:       os.Args[i+3],
			OriginalCommand: "pwd",
			CommandToRun:    "pwd",
		})
		if err == nil {
			fmt.Println("ok")
			os.Exit(0)
		}
		if errors.Is(err, ErrApprovalPending) {
			fmt.Println("pending")
			os.Exit(0)
		}
		fmt.Println(err)
		os.Exit(3)
	}
	os.Exit(2)
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("mode %s=%#o want %#o", path, got, want)
	}
}
