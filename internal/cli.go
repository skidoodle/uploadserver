package internal

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"
)

const CLIUsage = `usage: uploadserver <command> [<args>]

Commands:
  run                        Start the web server
  list                       List all tokens, with usage and quotas
  info <id>                  Show detailed status and usage for a single token
  add [--label L] [--role R] Create a new token
  rm <id>                    Delete a token
  disable <id>               Disable a token
  enable <id>                Enable a token
  limit <id> [flags]         Set upload quotas for a token (use --help for flags)
  global [flags]             Show or set the server-wide default quota
  scan [--token ID]          Find untracked files on disk and optionally import them
  migrate --token <id>       Move flat uploads into per-user directories
  prune [--days N] [--dry-run] Purge temporary upload files older than N days
  export [--out file.json]   Export token store metadata to JSON
  import [--in file.json]    Import token store metadata from JSON
  dump                       Decode the binary store and print everything in it
  reset                      Delete all tokens and reset store
  version                    Show version and runtime info`

// IndexUpdater allows CLI commands to synchronize live in-memory reverse file indexes.
type IndexUpdater interface {
	Add(filename, tokenID string)
	Remove(filename string)
	RemoveAll(tokenID string) []string
}

// ExecutionContext encapsulates the dependencies and streams for running a CLI command.
type ExecutionContext struct {
	Store     *TokenStore
	Index     IndexUpdater
	UploadDir string
	Stdout    io.Writer
	Stderr    io.Writer
	IsIPC     bool
}

// RunTokenCLI handles the CLI subcommands, seamlessly routing between the live server
// over the control socket (online) or direct database access on disk (offline).
func RunTokenCLI(args []string) (err error) {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(os.Stderr, CLIUsage)
		return errors.New("no subcommand given")
	}

	if args[0] == "version" {
		fmt.Println(VersionString())
		return nil
	}

	storePath := Env("TOKEN_STORE", "./state/tokens.db")
	sockPath := SocketPath(storePath)

	// Attempt online IPC execution if control socket exists and server is running.
	handled, err := SendIPCCommand(sockPath, args, os.Stdout, os.Stderr)
	if handled {
		return err
	}

	// Fall back to direct offline execution.
	return RunOfflineCLI(storePath, args, os.Stdout, os.Stderr)
}

// RunOfflineCLI executes CLI subcommands directly against the on-disk database.
func RunOfflineCLI(storePath string, args []string, stdout, stderr io.Writer) (err error) {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stderr, CLIUsage)
		return errors.New("no subcommand given")
	}

	// reset operates directly on the raw file and must work even if unparseable.
	if args[0] == "reset" {
		err := os.Remove(storePath)
		if errors.Is(err, fs.ErrNotExist) {
			_, _ = fmt.Fprintf(stdout, "no token store at %s — nothing to reset\n", storePath)
			return nil
		}
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(stdout, "removed %s\n", storePath)
		_, _ = fmt.Fprintln(stdout, "start the server again to generate a fresh admin token")
		return nil
	}

	uploadDir := Env("UPLOAD_DIR", "./data")

	// prune operates purely on the upload directory without requiring database access.
	if args[0] == "prune" {
		execCtx := ExecutionContext{
			UploadDir: uploadDir,
			Stdout:    stdout,
			Stderr:    stderr,
			IsIPC:     false,
		}
		return runPrune(execCtx, args[1:])
	}

	var store *TokenStore
	store, err = OpenStore(storePath)
	if err != nil {
		if errors.Is(err, ErrLocked) {
			sockPath := SocketPath(storePath)
			return fmt.Errorf("token store %s is locked by another process (and control socket %s was unreachable): %w", storePath, sockPath, err)
		}
		return err
	}
	defer func() {
		if cerr := store.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()

	execCtx := ExecutionContext{
		Store:     store,
		UploadDir: uploadDir,
		Stdout:    stdout,
		Stderr:    stderr,
		IsIPC:     false,
	}

	return RunCommand(execCtx, args)
}

// RunCommand dispatches a subcommand against an initialized ExecutionContext.
func RunCommand(ctx ExecutionContext, args []string) error {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(ctx.Stderr, CLIUsage)
		return errors.New("no subcommand given")
	}

	switch args[0] {
	case "version":
		_, _ = fmt.Fprintln(ctx.Stdout, VersionString())
		return nil

	case "list":
		return runList(ctx)

	case "info":
		return runInfo(ctx, args[1:])

	case "add":
		return runAdd(ctx, args[1:])

	case "rm":
		return runRm(ctx, args[1:])

	case "disable":
		return runDisable(ctx, args[1:])

	case "enable":
		return runEnable(ctx, args[1:])

	case "limit":
		return runLimit(ctx, args[1:])

	case "global":
		return runGlobal(ctx, args[1:])

	case "scan":
		return runScan(ctx, args[1:])

	case "migrate":
		return runMigrate(ctx, args[1:])

	case "prune":
		return runPrune(ctx, args[1:])

	case "export":
		return runExport(ctx, args[1:])

	case "import":
		return runImport(ctx, args[1:])

	case "dump":
		return runDump(ctx)

	case "reset":
		if ctx.IsIPC {
			return errors.New("cannot reset token store while the server is running; stop the server first")
		}
		if ctx.Store != nil {
			storePath := ctx.Store.Path()
			if storePath != "" {
				_ = ctx.Store.Close()
				err := os.Remove(storePath)
				if errors.Is(err, fs.ErrNotExist) {
					_, _ = fmt.Fprintf(ctx.Stdout, "no token store at %s — nothing to reset\n", storePath)
					return nil
				}
				if err != nil {
					return err
				}
				_, _ = fmt.Fprintf(ctx.Stdout, "removed %s\n", storePath)
				_, _ = fmt.Fprintln(ctx.Stdout, "start the server again to generate a fresh admin token")
				return nil
			}
		}
		return errors.New("store path unavailable for reset")

	default:
		_, _ = fmt.Fprintln(ctx.Stderr, CLIUsage)
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func runList(ctx ExecutionContext) error {
	if ctx.Store == nil {
		return errors.New("store not available")
	}
	_, _ = fmt.Fprintf(ctx.Stdout, "global default: %s\n", limitSummary(ctx.Store.GlobalLimits()))
	tw := tabwriter.NewWriter(ctx.Stdout, 0, 2, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "ID\tROLE\tSTATUS\tUPLOADS\tSIZE\tQUOTA\tLAST USED\tLABEL")
	for _, r := range ctx.Store.List() {
		status := "enabled"
		if r.Disabled {
			status = "disabled"
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			r.ID, r.Role, status, Comma(r.Usage.Uploads), FormatSize(r.Usage.Bytes),
			quotaColumn(r), fmtTime(r.LastUsed), r.Label)
	}
	return tw.Flush()
}

func runInfo(ctx ExecutionContext, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: uploadserver info <id>")
	}
	if ctx.Store == nil {
		return errors.New("store not available")
	}
	id := args[0]
	rec, ok := ctx.Store.GetRecord(id)
	if !ok {
		return ErrNotFound
	}
	_, _ = fmt.Fprintf(ctx.Stdout, "Token ID:    %s\n", rec.ID)
	_, _ = fmt.Fprintf(ctx.Stdout, "Label:       %s\n", rec.Label)
	_, _ = fmt.Fprintf(ctx.Stdout, "Role:        %s\n", rec.Role)
	_, _ = fmt.Fprintf(ctx.Stdout, "Status:      %s\n", map[bool]string{true: "disabled", false: "enabled"}[rec.Disabled])
	_, _ = fmt.Fprintf(ctx.Stdout, "Created:     %s\n", fmtTime(rec.CreatedAt))
	_, _ = fmt.Fprintf(ctx.Stdout, "Last Used:   %s\n", fmtTime(rec.LastUsed))
	_, _ = fmt.Fprintf(ctx.Stdout, "Uploads:     %s\n", Comma(rec.Usage.Uploads))
	_, _ = fmt.Fprintf(ctx.Stdout, "Total Bytes: %s\n", FormatSize(rec.Usage.Bytes))
	_, _ = fmt.Fprintf(ctx.Stdout, "Month Usage: %s / %s\n", Comma(rec.Usage.MonthUploads), FormatSize(rec.Usage.MonthBytes))
	_, _ = fmt.Fprintf(ctx.Stdout, "Invites:     %d\n", rec.Invites)
	_, _ = fmt.Fprintf(ctx.Stdout, "Quota Caps:  %s\n", quotaColumn(rec))
	return nil
}

func runAdd(ctx ExecutionContext, args []string) error {
	if ctx.Store == nil {
		return errors.New("store not available")
	}
	fs := flag.NewFlagSet("add", flag.ContinueOnError)
	fs.SetOutput(ctx.Stderr)
	label := fs.String("label", "", "human-readable label")
	role := fs.String("role", RoleUpload, "token role: upload or admin")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *role == RoleRoot {
		return errors.New("root tokens are generated only on first run; run `token reset` then restart to mint a new one")
	}
	id, secret, err := ctx.Store.Add(*label, *role)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(ctx.Stdout, "created %s token %s\n", *role, id)
	_, _ = fmt.Fprintf(ctx.Stdout, "secret (shown once): %s\n", secret)
	return nil
}

func runRm(ctx ExecutionContext, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: uploadserver rm <id>")
	}
	if ctx.Store == nil {
		return errors.New("store not available")
	}
	id := args[0]
	if err := ctx.Store.Remove(id); err != nil {
		return err
	}
	if ctx.Index != nil {
		ctx.Index.RemoveAll(id)
	}
	return nil
}

func runDisable(ctx ExecutionContext, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: uploadserver disable <id>")
	}
	if ctx.Store == nil {
		return errors.New("store not available")
	}
	return ctx.Store.SetDisabled(args[0], true)
}

func runEnable(ctx ExecutionContext, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: uploadserver enable <id>")
	}
	if ctx.Store == nil {
		return errors.New("store not available")
	}
	return ctx.Store.SetDisabled(args[0], false)
}

// quotaFlags registers the four quota dimensions on a flag set, returning a
// function that folds the flags the operator actually passed onto base, leaving
// the rest untouched (partial updates). Sizes accept units (e.g. 5GB).
func quotaFlags(fs *flag.FlagSet) func(base Limits) (Limits, error) {
	totalSize := fs.String("total-size", "", "lifetime size cap, e.g. 5GB (0 to clear)")
	totalUploads := fs.Int64("total-uploads", 0, "lifetime upload-count cap (0 to clear)")
	monthlySize := fs.String("monthly-size", "", "size cap per calendar month (0 to clear)")
	monthlyUploads := fs.Int64("monthly-uploads", 0, "upload-count cap per calendar month (0 to clear)")

	return func(base Limits) (Limits, error) {
		set := map[string]bool{}
		fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
		if set["total-size"] {
			size, err := ParseSize(*totalSize)
			if err != nil {
				return base, err
			}
			base.MaxBytes = size
		}
		if set["monthly-size"] {
			size, err := ParseSize(*monthlySize)
			if err != nil {
				return base, err
			}
			base.MonthlyBytes = size
		}
		if set["total-uploads"] {
			base.MaxUploads = *totalUploads
		}
		if set["monthly-uploads"] {
			base.MonthlyUploads = *monthlyUploads
		}
		return base, nil
	}
}

// runLimit sets a token's personal quotas and bypass flag.
func runLimit(ctx ExecutionContext, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: uploadserver limit <id> [flags]")
	}
	if ctx.Store == nil {
		return errors.New("store not available")
	}
	id := args[0]

	fs := flag.NewFlagSet("limit", flag.ContinueOnError)
	fs.SetOutput(ctx.Stderr)
	apply := quotaFlags(fs)
	bypass := fs.Bool("bypass", false, "ignore the global quota for this token (-bypass=false to re-enable it)")
	clear := fs.Bool("clear", false, "remove every quota and the bypass flag from the token")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}

	lim, bypassNow, ok := ctx.Store.LimitsOf(id)
	if !ok {
		return ErrNotFound
	}
	if *clear {
		lim, bypassNow = Limits{}, false
	}

	lim, err := apply(lim)
	if err != nil {
		return err
	}
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "bypass" {
			bypassNow = *bypass
		}
	})

	if err := ctx.Store.SetLimits(id, lim, bypassNow); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(ctx.Stdout, "quotas for %s: %s\n", id, quotaColumn(TokenRecord{Limits: lim, Bypass: bypassNow}))
	return nil
}

// runGlobal shows or sets the server-wide default quota.
func runGlobal(ctx ExecutionContext, args []string) error {
	if ctx.Store == nil {
		return errors.New("store not available")
	}
	fs := flag.NewFlagSet("global", flag.ContinueOnError)
	fs.SetOutput(ctx.Stderr)
	apply := quotaFlags(fs)
	clear := fs.Bool("clear", false, "remove the global quota entirely")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if fs.NFlag() == 0 {
		_, _ = fmt.Fprintf(ctx.Stdout, "global default: %s\n", limitSummary(ctx.Store.GlobalLimits()))
		return nil
	}

	base := ctx.Store.GlobalLimits()
	if *clear {
		base = Limits{}
	}
	lim, err := apply(base)
	if err != nil {
		return err
	}
	if err := ctx.Store.SetGlobalLimits(lim); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(ctx.Stdout, "global default: %s\n", limitSummary(lim))
	return nil
}

// runDump prints the store records and fields.
func runDump(ctx ExecutionContext) error {
	if ctx.Store == nil {
		return errors.New("store not available")
	}
	storePath := ctx.Store.Path()
	size := "?"
	if storePath != "" {
		if fi, err := os.Stat(storePath); err == nil {
			size = FormatSize(fi.Size())
		}
	}
	recs := ctx.Store.records()
	if storePath != "" {
		_, _ = fmt.Fprintf(ctx.Stdout, "%s — %s on disk, %d token(s)\n", storePath, size, len(recs))
	} else {
		_, _ = fmt.Fprintf(ctx.Stdout, "%d token(s)\n", len(recs))
	}
	_, _ = fmt.Fprintf(ctx.Stdout, "global default: %s\n", limitSummary(ctx.Store.GlobalLimits()))

	tw := tabwriter.NewWriter(ctx.Stdout, 0, 2, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "ID\tROLE\tSTATUS\tLABEL\tUPLOADS\tSIZE\tMONTH\tQUOTA\tCREATED\tLAST USED\tHASH")
	for _, r := range recs {
		status := "enabled"
		if r.Disabled {
			status = "disabled"
		}
		month := fmt.Sprintf("%s / %s", Comma(r.Usage.MonthUploads), FormatSize(r.Usage.MonthBytes))
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			r.ID, r.Role, status, r.Label, Comma(r.Usage.Uploads), FormatSize(r.Usage.Bytes),
			month, quotaColumn(r), fmtTime(r.CreatedAt), fmtTime(r.LastUsed), shortHash(r.Hash))
	}
	return tw.Flush()
}

func shortHash(h string) string {
	if h == "" {
		return "-"
	}
	if len(h) > 12 {
		return h[:12] + "…"
	}
	return h
}

func quotaColumn(r TokenRecord) string {
	if r.Bypass {
		return "exempt"
	}
	return limitSummary(r.Limits)
}

func limitSummary(l Limits) string {
	if s := SummarizeLimits(l); s != "" {
		return s
	}
	return "-"
}

func fmtTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return ToLocalTime(t).Format("2006-01-02 15:04")
}

// runScan scans UPLOAD_DIR for files not tracked in any token's upload history.
func runScan(ctx ExecutionContext, args []string) error {
	if ctx.Store == nil {
		return errors.New("store not available")
	}
	fs := flag.NewFlagSet("scan", flag.ContinueOnError)
	fs.SetOutput(ctx.Stderr)
	tokenID := fs.String("token", "", "token ID to import untracked files into (omit for dry-run list)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	uploadDir := ctx.UploadDir
	if uploadDir == "" {
		uploadDir = "./data"
	}

	allEntries, err := ctx.Store.AllUploadEntries()
	if err != nil {
		return fmt.Errorf("read upload entries: %w", err)
	}
	tracked := make(map[string]bool)
	for _, entries := range allEntries {
		for _, e := range entries {
			tracked[filepath.ToSlash(e.Name)] = true
			tracked[filepath.Base(e.Name)] = true
		}
	}

	dirEntries, err := os.ReadDir(uploadDir)
	if err != nil {
		return fmt.Errorf("read %s: %w", uploadDir, err)
	}

	type untrackedFile struct {
		displayPath string
		storedName  string
		sourcePath  string
		size        int64
		modTime     time.Time
	}
	var untracked []untrackedFile

	for _, de := range dirEntries {
		if de.IsDir() || strings.HasPrefix(de.Name(), ".") {
			continue
		}
		if tracked[de.Name()] {
			continue
		}
		info, err := de.Info()
		if err != nil {
			continue
		}
		untracked = append(untracked, untrackedFile{
			displayPath: de.Name(),
			storedName:  de.Name(),
			sourcePath:  filepath.Join(uploadDir, de.Name()),
			size:        info.Size(),
			modTime:     info.ModTime().UTC(),
		})
	}

	for _, de := range dirEntries {
		if !de.IsDir() || strings.HasPrefix(de.Name(), ".") || strings.HasPrefix(de.Name(), "_") {
			continue
		}
		subEntries, err := os.ReadDir(filepath.Join(uploadDir, de.Name()))
		if err != nil {
			continue
		}
		for _, sub := range subEntries {
			if sub.IsDir() || strings.HasPrefix(sub.Name(), ".") {
				continue
			}
			relPath := filepath.ToSlash(filepath.Join(de.Name(), sub.Name()))
			if tracked[relPath] || tracked[sub.Name()] {
				continue
			}
			info, err := sub.Info()
			if err != nil {
				continue
			}
			untracked = append(untracked, untrackedFile{
				displayPath: relPath,
				storedName:  sub.Name(),
				sourcePath:  filepath.Join(uploadDir, de.Name(), sub.Name()),
				size:        info.Size(),
				modTime:     info.ModTime().UTC(),
			})
		}
	}

	if len(untracked) == 0 {
		_, _ = fmt.Fprintln(ctx.Stdout, "all files on disk are already tracked — nothing to import")
		return nil
	}

	_, _ = fmt.Fprintf(ctx.Stdout, "found %d untracked file(s) in %s:\n\n", len(untracked), uploadDir)
	tw := tabwriter.NewWriter(ctx.Stdout, 0, 2, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "FILE\tSIZE\tMODIFIED")
	for _, f := range untracked {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\n", f.displayPath, FormatSize(f.size), fmtTime(f.modTime))
	}
	_ = tw.Flush()

	if *tokenID == "" {
		_, _ = fmt.Fprintln(ctx.Stdout, "\nto import these files, re-run with --token <id>")
		return nil
	}

	targetDir := filepath.Join(uploadDir, *tokenID)
	if err := os.MkdirAll(targetDir, 0o750); err != nil {
		return fmt.Errorf("create user dir: %w", err)
	}

	var entries []UploadEntry
	for _, f := range untracked {
		targetPath := filepath.Join(targetDir, f.storedName)
		if f.sourcePath != targetPath {
			// If file is not already in the target directory, move it in.
			if _, statErr := os.Stat(targetPath); statErr == nil && f.sourcePath != targetPath {
				return fmt.Errorf("destination file %s already exists", targetPath)
			}
			if err := os.Rename(f.sourcePath, targetPath); err != nil {
				return fmt.Errorf("move %s to %s: %w", f.sourcePath, targetPath, err)
			}
		}

		entries = append(entries, UploadEntry{
			Name:       f.storedName,
			Size:       f.size,
			UploadedAt: f.modTime,
		})
	}

	if err := ctx.Store.ImportUploadEntries(*tokenID, entries); err != nil {
		return fmt.Errorf("import: %w", err)
	}
	if ctx.Index != nil {
		for _, e := range entries {
			ctx.Index.Add(e.Name, *tokenID)
		}
	}
	_, _ = fmt.Fprintf(ctx.Stdout, "\nimported %d file(s) into token %s\n", len(entries), *tokenID)
	return nil
}

// runMigrate moves existing flat files from UPLOAD_DIR into per-user subdirectories.
func runMigrate(ctx ExecutionContext, args []string) error {
	if ctx.Store == nil {
		return errors.New("store not available")
	}
	fs := flag.NewFlagSet("migrate", flag.ContinueOnError)
	fs.SetOutput(ctx.Stderr)
	tokenID := fs.String("token", "", "token ID to adopt all flat files into (required)")
	dryRun := fs.Bool("dry-run", false, "list files that would be moved without touching anything")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *tokenID == "" {
		return errors.New("usage: uploadserver migrate --token <id> [--dry-run]")
	}

	if _, ok := ctx.Store.GetRecord(*tokenID); !ok {
		return fmt.Errorf("token %q not found", *tokenID)
	}

	uploadDir := ctx.UploadDir
	if uploadDir == "" {
		uploadDir = "./data"
	}
	userDir := filepath.Join(uploadDir, *tokenID)

	dirEntries, err := os.ReadDir(uploadDir)
	if err != nil {
		return fmt.Errorf("read %s: %w", uploadDir, err)
	}

	type migFile struct {
		name    string
		size    int64
		modTime time.Time
	}
	var toMove []migFile
	for _, de := range dirEntries {
		if de.IsDir() || strings.HasPrefix(de.Name(), ".") {
			continue
		}
		info, err := de.Info()
		if err != nil {
			continue
		}
		toMove = append(toMove, migFile{
			name:    de.Name(),
			size:    info.Size(),
			modTime: info.ModTime().UTC(),
		})
	}

	if len(toMove) == 0 {
		_, _ = fmt.Fprintln(ctx.Stdout, "no flat files found to migrate")
		return nil
	}

	_, _ = fmt.Fprintf(ctx.Stdout, "found %d file(s) in %s to migrate into %s/\n", len(toMove), uploadDir, *tokenID)

	if *dryRun {
		tw := tabwriter.NewWriter(ctx.Stdout, 0, 2, 2, ' ', 0)
		_, _ = fmt.Fprintln(tw, "FILE\tSIZE\tMODIFIED")
		for _, f := range toMove {
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\n", f.name, FormatSize(f.size), fmtTime(f.modTime))
		}
		_ = tw.Flush()
		_, _ = fmt.Fprintf(ctx.Stdout, "\n[dry-run] would move %d file(s) — re-run without --dry-run to execute\n", len(toMove))
		return nil
	}

	if err := os.MkdirAll(userDir, 0o750); err != nil {
		return fmt.Errorf("create user dir: %w", err)
	}

	allEntries, err := ctx.Store.AllUploadEntries()
	if err != nil {
		return fmt.Errorf("read upload entries: %w", err)
	}
	tracked := make(map[string]bool)
	for _, entries := range allEntries {
		for _, e := range entries {
			tracked[e.Name] = true
		}
	}

	var moved int
	var newEntries []UploadEntry
	for _, f := range toMove {
		src := filepath.Join(uploadDir, f.name)
		dst := filepath.Join(userDir, f.name)
		if err := os.Rename(src, dst); err != nil {
			_, _ = fmt.Fprintf(ctx.Stderr, "failed to move %s: %v\n", f.name, err)
			continue
		}
		moved++
		if !tracked[f.name] {
			newEntries = append(newEntries, UploadEntry{
				Name:       f.name,
				Size:       f.size,
				UploadedAt: f.modTime,
			})
		}
	}

	if len(newEntries) > 0 {
		if err := ctx.Store.ImportUploadEntries(*tokenID, newEntries); err != nil {
			return fmt.Errorf("import entries: %w", err)
		}
	}
	if ctx.Index != nil {
		for _, f := range toMove {
			ctx.Index.Add(f.name, *tokenID)
		}
	}

	_, _ = fmt.Fprintf(ctx.Stdout, "\nmigrated %d file(s) into %s/%s\n", moved, uploadDir, *tokenID)
	if len(newEntries) > 0 {
		_, _ = fmt.Fprintf(ctx.Stdout, "imported %d previously untracked file(s) into token %s\n", len(newEntries), *tokenID)
	}
	return nil
}

// runPrune prunes temporary upload files older than a specified number of days.
func runPrune(ctx ExecutionContext, args []string) error {
	fs := flag.NewFlagSet("prune", flag.ContinueOnError)
	fs.SetOutput(ctx.Stderr)
	days := fs.Int("days", 1, "purge temp files older than N days")
	dryRun := fs.Bool("dry-run", false, "list files without deleting")
	if err := fs.Parse(args); err != nil {
		return err
	}

	uploadDir := ctx.UploadDir
	if uploadDir == "" {
		uploadDir = "./data"
	}
	cutoff := time.Now().Add(-time.Duration(*days) * 24 * time.Hour)

	dirEntries, err := os.ReadDir(uploadDir)
	if err != nil {
		return fmt.Errorf("read %s: %w", uploadDir, err)
	}

	var prunedCount int
	var prunedBytes int64

	for _, de := range dirEntries {
		if de.IsDir() || !strings.HasPrefix(de.Name(), ".upload-") {
			continue
		}
		info, err := de.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			filePath := filepath.Join(uploadDir, de.Name())
			if *dryRun {
				_, _ = fmt.Fprintf(ctx.Stdout, "[dry-run] would delete %s (%s, mod: %s)\n", de.Name(), FormatSize(info.Size()), fmtTime(info.ModTime()))
			} else {
				if err := os.Remove(filePath); err != nil {
					_, _ = fmt.Fprintf(ctx.Stderr, "failed to delete %s: %v\n", de.Name(), err)
					continue
				}
				_, _ = fmt.Fprintf(ctx.Stdout, "deleted %s (%s)\n", de.Name(), FormatSize(info.Size()))
			}
			prunedCount++
			prunedBytes += info.Size()
		}
	}

	if prunedCount == 0 {
		_, _ = fmt.Fprintln(ctx.Stdout, "no temporary files due for pruning")
		return nil
	}

	if *dryRun {
		_, _ = fmt.Fprintf(ctx.Stdout, "\n[dry-run] %d temp file(s) eligible for pruning (%s total)\n", prunedCount, FormatSize(prunedBytes))
	} else {
		_, _ = fmt.Fprintf(ctx.Stdout, "\npruned %d temp file(s) (%s freed)\n", prunedCount, FormatSize(prunedBytes))
	}
	return nil
}

type exportData struct {
	Global Limits        `json:"global"`
	Tokens []TokenRecord `json:"tokens"`
}

func runExport(ctx ExecutionContext, args []string) error {
	if ctx.Store == nil {
		return errors.New("store not available")
	}
	fs := flag.NewFlagSet("export", flag.ContinueOnError)
	fs.SetOutput(ctx.Stderr)
	outFile := fs.String("out", "", "output file path (default stdout)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	data := exportData{
		Global: ctx.Store.GlobalLimits(),
		Tokens: ctx.Store.List(),
	}

	writer := io.Writer(ctx.Stdout)
	if *outFile != "" {
		f, err := os.Create(*outFile)
		if err != nil {
			return fmt.Errorf("create export file: %w", err)
		}
		defer func() { _ = f.Close() }()
		writer = f
	}

	enc := json.NewEncoder(writer)
	enc.SetIndent("", "  ")
	if err := enc.Encode(data); err != nil {
		return fmt.Errorf("encode export: %w", err)
	}

	if *outFile != "" {
		_, _ = fmt.Fprintf(ctx.Stdout, "exported %d token(s) to %s\n", len(data.Tokens), *outFile)
	}
	return nil
}

func runImport(ctx ExecutionContext, args []string) error {
	if ctx.Store == nil {
		return errors.New("store not available")
	}
	fs := flag.NewFlagSet("import", flag.ContinueOnError)
	fs.SetOutput(ctx.Stderr)
	inFile := fs.String("in", "", "input JSON file path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *inFile == "" {
		return errors.New("usage: uploadserver import --in <file.json>")
	}

	f, err := os.Open(*inFile)
	if err != nil {
		return fmt.Errorf("open import file: %w", err)
	}
	defer func() { _ = f.Close() }()

	var data exportData
	if err := json.NewDecoder(f).Decode(&data); err != nil {
		return fmt.Errorf("decode import file: %w", err)
	}

	if err := ctx.Store.SetGlobalLimits(data.Global); err != nil {
		return fmt.Errorf("set global limits: %w", err)
	}

	imported := 0
	for _, rec := range data.Tokens {
		if rec.ID == "" {
			continue
		}
		if err := ctx.Store.SetLimits(rec.ID, rec.Limits, rec.Bypass); err == nil {
			_ = ctx.Store.SetDisabled(rec.ID, rec.Disabled)
			_ = ctx.Store.SetInvites(rec.ID, rec.Invites)
			imported++
		}
	}

	_, _ = fmt.Fprintf(ctx.Stdout, "imported global quota and updated %d token(s) from %s\n", imported, *inFile)
	return nil
}
