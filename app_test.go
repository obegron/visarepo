package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadAllCommitsGitParallel(t *testing.T) {
	repoPath, hashes := makeTestRepository(t)

	cfg := Config{
		CommitLimit:   -1,
		RepoPath:      repoPath,
		ReportWorkers: 2,
	}
	_, commits, maxAdditions, maxDeletions, total, workers, err := loadAllCommitsGitParallel(cfg, nil)
	if err != nil {
		t.Fatalf("load commits: %v", err)
	}

	if total != 3 || workers != 2 || len(commits) != 3 {
		t.Fatalf("unexpected load sizes: total=%d workers=%d commits=%d", total, workers, len(commits))
	}
	for i, hash := range hashes {
		if commits[i].Hash != hash {
			t.Errorf("commit %d hash = %s, want %s", i, commits[i].Hash, hash)
		}
	}

	wantAdditions := []int{1, 1, 2}
	wantCumulative := []int{1, 2, 4}
	for i, commit := range commits {
		if commit.Files != 1 || commit.Additions != wantAdditions[i] || commit.Deletions != 0 {
			t.Errorf("commit %d stats = files:%d additions:%d deletions:%d", i, commit.Files, commit.Additions, commit.Deletions)
		}
		if commit.CumulativeAdditions != wantCumulative[i] {
			t.Errorf("commit %d cumulative additions = %d, want %d", i, commit.CumulativeAdditions, wantCumulative[i])
		}
	}
	if maxAdditions != 2 || maxDeletions != 0 {
		t.Errorf("max stats = additions:%d deletions:%d", maxAdditions, maxDeletions)
	}
}

func TestTimelineCommitLimitUsesStartOfHistory(t *testing.T) {
	repoPath, hashes := makeTestRepository(t)

	cfg := Config{
		CommitLimit:          2,
		RepoPath:             repoPath,
		ReportWorkers:        1,
		commitLimitFromStart: true,
	}
	_, commits, _, _, _, _, err := loadAllCommitsGitParallel(cfg, nil)
	if err != nil {
		t.Fatalf("load limited commits: %v", err)
	}
	if len(commits) != 2 {
		t.Fatalf("got %d commits, want 2", len(commits))
	}
	for i := range commits {
		if commits[i].Hash != hashes[i] {
			t.Errorf("commit %d hash = %s, want oldest hash %s", i, commits[i].Hash, hashes[i])
		}
	}
}

func TestProgressTimerStopsWhenLoadingCompletes(t *testing.T) {
	model := InitialModel(Config{
		AutoProgress:       true,
		ProgressIntervalMs: 50,
	})
	close(model.processedCommitsChan)

	updated, cmd := model.Update(progressTickMsg(time.Now()))
	result := updated.(*Model)
	if !result.loadingComplete {
		t.Fatal("closed commit channel did not mark loading complete")
	}
	if cmd != nil {
		t.Fatal("progress timer was rescheduled after loading completed")
	}
}

func makeTestRepository(t *testing.T) (string, []string) {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "init", "-q")
	runGit(t, dir, "config", "user.name", "Test Author")
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "commit.gpgsign", "false")

	writeTestFile(t, filepath.Join(dir, "first.txt"), "one\n")
	runGit(t, dir, "add", "first.txt")
	runGit(t, dir, "commit", "-qm", "first")

	writeTestFile(t, filepath.Join(dir, "first.txt"), "one\ntwo\n")
	runGit(t, dir, "add", "first.txt")
	runGit(t, dir, "commit", "-qm", "second")

	writeTestFile(t, filepath.Join(dir, "second.txt"), "three\nfour\n")
	runGit(t, dir, "add", "second.txt")
	runGit(t, dir, "commit", "-qm", "third")

	output := runGit(t, dir, "rev-list", "--reverse", "HEAD")
	return dir, strings.Fields(output)
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
	return strings.TrimSpace(string(output))
}
