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
	"runtime"
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

// RunTokenCLI handles the CLI subcommands, operating directly on the on-disk store.
// The store path is determined by the TOKEN_STORE environment variable, or defaults to "./state/tokens.db".
func RunTokenCLI(args []string) (err error) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, CLIUsage)
		return errors.New("no subcommand given")
	}

	if args[0] == "version" {
		fmt.Printf("uploadserver v1.0.0 (%s %s/%s)\n", runtime.Version(), runtime.GOOS, runtime.GOARCH)
		return nil
	}

	storePath := Env("TOKEN_STORE", "./state/tokens.db")

	// reset operates on the raw file and must work even if it is unparseable.
	if args[0] == "reset" {
		err := os.Remove(storePath)
		if errors.Is(err, fs.ErrNotExist) {
			fmt.Printf("no token store at %s — nothing to reset\n", storePath)
			return nil
		}
		if err != nil {
			return err
		}
		fmt.Printf("removed %s\n", storePath)
		fmt.Println("start the server again to generate a fresh admin token")
		return nil
	}

	// dump decodes the raw file straight off disk so what it prints is exactly
	// what is stored, hashes included — the human-readable window into a binary file.
	if args[0] == "dump" {
		return runDump(storePath)
	}

	if args[0] == "prune" {
		return runPrune(args[1:])
	}

	var store *TokenStore
	store, err = OpenStore(storePath)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := store.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()

	switch args[0] {
	case "list":
		fmt.Printf("global default: %s\n", limitSummary(store.GlobalLimits()))
		tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
		_, _ = fmt.Fprintln(tw, "ID\tROLE\tSTATUS\tUPLOADS\tSIZE\tQUOTA\tLAST USED\tLABEL")
		for _, r := range store.List() {
			status := "enabled"
			if r.Disabled {
				status = "disabled"
			}
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
				r.ID, r.Role, status, Comma(r.Usage.Uploads), FormatSize(r.Usage.Bytes),
				quotaColumn(r), fmtTime(r.LastUsed), r.Label)
		}
		return tw.Flush()

	case "info":
		if len(args) < 2 {
			return errors.New("usage: uploadserver info <id>")
		}
		return runInfo(store, args[1])

	case "add":
		fs := flag.NewFlagSet("add", flag.ContinueOnError)
		label := fs.String("label", "", "human-readable label")
		role := fs.String("role", RoleUpload, "token role: upload or admin")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *role == RoleRoot {
			return errors.New("root tokens are generated only on first run; run `token reset` then restart to mint a new one")
		}
		id, secret, err := store.Add(*label, *role)
		if err != nil {
			return err
		}
		fmt.Printf("created %s token %s\n", *role, id)
		fmt.Printf("secret (shown once): %s\n", secret)
		return nil

	case "rm":
		if len(args) < 2 {
			return errors.New("usage: uploadserver rm <id>")
		}
		return store.Remove(args[1])

	case "disable":
		if len(args) < 2 {
			return errors.New("usage: uploadserver disable <id>")
		}
		return store.SetDisabled(args[1], true)

	case "enable":
		if len(args) < 2 {
			return errors.New("usage: uploadserver enable <id>")
		}
		return store.SetDisabled(args[1], false)

	case "limit":
		return runLimit(store, args[1:])

	case "global":
		return runGlobal(store, args[1:])

	case "scan":
		return runScan(store, args[1:])

	case "migrate":
		return runMigrate(store, args[1:])

	case "export":
		return runExport(store, args[1:])

	case "import":
		return runImport(store, args[1:])

	default:
		fmt.Fprintln(os.Stderr, CLIUsage)
		return fmt.Errorf("unknown command %q", args[0])
	}
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

// runLimit sets a token's personal quotas and bypass flag. Only the flags
// actually passed are changed; --clear wipes every quota and the bypass flag.
func runLimit(store *TokenStore, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: uploadserver limit <id> [flags]")
	}
	id := args[0]

	fs := flag.NewFlagSet("limit", flag.ContinueOnError)
	apply := quotaFlags(fs)
	bypass := fs.Bool("bypass", false, "ignore the global quota for this token (-bypass=false to re-enable it)")
	clear := fs.Bool("clear", false, "remove every quota and the bypass flag from the token")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}

	lim, bypassNow, ok := store.LimitsOf(id)
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

	if err := store.SetLimits(id, lim, bypassNow); err != nil {
		return err
	}
	fmt.Printf("quotas for %s: %s\n", id, quotaColumn(TokenRecord{Limits: lim, Bypass: bypassNow}))
	return nil
}

// runGlobal shows or sets the server-wide default quota. With no flags it just
// prints the current value.
func runGlobal(store *TokenStore, args []string) error {
	fs := flag.NewFlagSet("global", flag.ContinueOnError)
	apply := quotaFlags(fs)
	clear := fs.Bool("clear", false, "remove the global quota entirely")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if fs.NFlag() == 0 {
		fmt.Printf("global default: %s\n", limitSummary(store.GlobalLimits()))
		return nil
	}

	base := store.GlobalLimits()
	if *clear {
		base = Limits{}
	}
	lim, err := apply(base)
	if err != nil {
		return err
	}
	if err := store.SetGlobalLimits(lim); err != nil {
		return err
	}
	fmt.Printf("global default: %s\n", limitSummary(lim))
	return nil
}

// runDump opens the bbolt store and prints every field it holds, hashes
// included. Unlike `list` (which strips hashes for safety), this is the faithful
// "look inside" view — the tool you reach for when you want to see what a binary
// database file you can't `cat` actually contains.
func runDump(path string) (err error) {
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		fmt.Printf("no token store at %s\n", path)
		return nil
	}

	var store *TokenStore
	store, err = OpenStore(path)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := store.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()

	size := "?"
	if fi, err := os.Stat(path); err == nil {
		size = FormatSize(fi.Size())
	}
	recs := store.records()
	fmt.Printf("%s — %s on disk, %d token(s)\n", path, size, len(recs))
	fmt.Printf("global default: %s\n", limitSummary(store.GlobalLimits()))

	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
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

// shortHash trims a stored hash to a recognisable prefix so the dump table stays
// readable; the full 64 hex characters would dwarf every other column.
func shortHash(h string) string {
	if h == "" {
		return "-"
	}
	if len(h) > 12 {
		return h[:12] + "…"
	}
	return h
}

// quotaColumn renders a token's quota state for the list view: "exempt" when it
// bypasses all quotas, its personal caps when set, or "-" when it simply
// inherits the global default.
func quotaColumn(r TokenRecord) string {
	if r.Bypass {
		return "exempt"
	}
	return limitSummary(r.Limits)
}

// limitSummary renders a quota as a compact one-line string, or "-" when it is
// unlimited.
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
	return t.Format("2006-01-02 15:04")
}

// runScan scans UPLOAD_DIR for files not tracked in any token's upload history
// and optionally imports them into a specific token.
func runScan(store *TokenStore, args []string) error {
	fs := flag.NewFlagSet("scan", flag.ContinueOnError)
	tokenID := fs.String("token", "", "token ID to import untracked files into (omit for dry-run list)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	uploadDir := Env("UPLOAD_DIR", "./data")

	// Read every tracked filename across all tokens.
	allEntries, err := store.AllUploadEntries()
	if err != nil {
		return fmt.Errorf("read upload entries: %w", err)
	}
	tracked := make(map[string]bool)
	for _, entries := range allEntries {
		for _, e := range entries {
			tracked[e.Name] = true
		}
	}

	// Walk the upload directory, including per-user subdirectories.
	dirEntries, err := os.ReadDir(uploadDir)
	if err != nil {
		return fmt.Errorf("read %s: %w", uploadDir, err)
	}

	type untrackedFile struct {
		name    string
		size    int64
		modTime time.Time
	}
	var untracked []untrackedFile

	// Check flat files in the root directory.
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
			name:    de.Name(),
			size:    info.Size(),
			modTime: info.ModTime().UTC(),
		})
	}

	// Check files inside per-user subdirectories.
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
			if tracked[sub.Name()] {
				continue
			}
			info, err := sub.Info()
			if err != nil {
				continue
			}
			untracked = append(untracked, untrackedFile{
				name:    de.Name() + "/" + sub.Name(),
				size:    info.Size(),
				modTime: info.ModTime().UTC(),
			})
		}
	}

	if len(untracked) == 0 {
		fmt.Println("all files on disk are already tracked — nothing to import")
		return nil
	}

	fmt.Printf("found %d untracked file(s) in %s:\n\n", len(untracked), uploadDir)
	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "FILE\tSIZE\tMODIFIED")
	for _, f := range untracked {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\n", f.name, FormatSize(f.size), fmtTime(f.modTime))
	}
	_ = tw.Flush()

	// Dry-run.
	if *tokenID == "" {
		fmt.Println("\nto import these files, re-run with --token <id>")
		return nil
	}

	// Import into the chosen token.
	var entries []UploadEntry
	for _, f := range untracked {
		entries = append(entries, UploadEntry{
			Name:       f.name,
			Size:       f.size,
			UploadedAt: f.modTime,
		})
	}

	if err := store.ImportUploadEntries(*tokenID, entries); err != nil {
		return fmt.Errorf("import: %w", err)
	}
	fmt.Printf("\nimported %d file(s) into token %s\n", len(entries), *tokenID)
	return nil
}

// runMigrate moves existing flat files from UPLOAD_DIR into per-user subdirectories
// (UPLOAD_DIR/<tokenID>/). All files in the flat directory are moved into the chosen
// token's folder and imported into its upload history.
func runMigrate(store *TokenStore, args []string) error {
	fs := flag.NewFlagSet("migrate", flag.ContinueOnError)
	tokenID := fs.String("token", "", "token ID to adopt all flat files into (required)")
	dryRun := fs.Bool("dry-run", false, "list files that would be moved without touching anything")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *tokenID == "" {
		return errors.New("usage: uploadserver migrate --token <id> [--dry-run]")
	}

	// Verify the token exists.
	if _, ok := store.GetRecord(*tokenID); !ok {
		return fmt.Errorf("token %q not found", *tokenID)
	}

	uploadDir := Env("UPLOAD_DIR", "./data")
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
		fmt.Println("no flat files found to migrate")
		return nil
	}

	fmt.Printf("found %d file(s) in %s to migrate into %s/\n", len(toMove), uploadDir, *tokenID)

	if *dryRun {
		tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
		_, _ = fmt.Fprintln(tw, "FILE\tSIZE\tMODIFIED")
		for _, f := range toMove {
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\n", f.name, FormatSize(f.size), fmtTime(f.modTime))
		}
		_ = tw.Flush()
		fmt.Printf("\n[dry-run] would move %d file(s) — re-run without --dry-run to execute\n", len(toMove))
		return nil
	}

	// Create the user directory.
	if err := os.MkdirAll(userDir, 0o750); err != nil {
		return fmt.Errorf("create user dir: %w", err)
	}

	// Read existing tracked files to avoid double-importing.
	allEntries, err := store.AllUploadEntries()
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
			fmt.Fprintf(os.Stderr, "failed to move %s: %v\n", f.name, err)
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

	// Import untracked files into the token's upload history.
	if len(newEntries) > 0 {
		if err := store.ImportUploadEntries(*tokenID, newEntries); err != nil {
			return fmt.Errorf("import entries: %w", err)
		}
	}

	fmt.Printf("\nmigrated %d file(s) into %s/%s\n", moved, uploadDir, *tokenID)
	if len(newEntries) > 0 {
		fmt.Printf("imported %d previously untracked file(s) into token %s\n", len(newEntries), *tokenID)
	}
	return nil
}

// runInfo prints information about a token.
func runInfo(store *TokenStore, id string) error {
	rec, ok := store.GetRecord(id)
	if !ok {
		return ErrNotFound
	}
	fmt.Printf("Token ID:    %s\n", rec.ID)
	fmt.Printf("Label:       %s\n", rec.Label)
	fmt.Printf("Role:        %s\n", rec.Role)
	fmt.Printf("Status:      %s\n", map[bool]string{true: "disabled", false: "enabled"}[rec.Disabled])
	fmt.Printf("Created:     %s\n", fmtTime(rec.CreatedAt))
	fmt.Printf("Last Used:   %s\n", fmtTime(rec.LastUsed))
	fmt.Printf("Uploads:     %s\n", Comma(rec.Usage.Uploads))
	fmt.Printf("Total Bytes: %s\n", FormatSize(rec.Usage.Bytes))
	fmt.Printf("Month Usage: %s / %s\n", Comma(rec.Usage.MonthUploads), FormatSize(rec.Usage.MonthBytes))
	fmt.Printf("Invites:     %d\n", rec.Invites)
	fmt.Printf("Quota Caps:  %s\n", quotaColumn(rec))
	return nil
}

// runPrune prunes temporary upload files older than a specified number of days.
func runPrune(args []string) error {
	fs := flag.NewFlagSet("prune", flag.ContinueOnError)
	days := fs.Int("days", 1, "purge temp files older than N days")
	dryRun := fs.Bool("dry-run", false, "list files without deleting")
	if err := fs.Parse(args); err != nil {
		return err
	}

	uploadDir := Env("UPLOAD_DIR", "./data")
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
				fmt.Printf("[dry-run] would delete %s (%s, mod: %s)\n", de.Name(), FormatSize(info.Size()), fmtTime(info.ModTime()))
			} else {
				if err := os.Remove(filePath); err != nil {
					fmt.Fprintf(os.Stderr, "failed to delete %s: %v\n", de.Name(), err)
					continue
				}
				fmt.Printf("deleted %s (%s)\n", de.Name(), FormatSize(info.Size()))
			}
			prunedCount++
			prunedBytes += info.Size()
		}
	}

	if prunedCount == 0 {
		fmt.Println("no temporary files due for pruning")
		return nil
	}

	if *dryRun {
		fmt.Printf("\n[dry-run] %d temp file(s) eligible for pruning (%s total)\n", prunedCount, FormatSize(prunedBytes))
	} else {
		fmt.Printf("\npruned %d temp file(s) (%s freed)\n", prunedCount, FormatSize(prunedBytes))
	}
	return nil
}

// runExport exports the token store to a JSON file.
type exportData struct {
	Global Limits        `json:"global"`
	Tokens []TokenRecord `json:"tokens"`
}

// runExport exports the token store to a JSON file.
func runExport(store *TokenStore, args []string) error {
	fs := flag.NewFlagSet("export", flag.ContinueOnError)
	outFile := fs.String("out", "", "output file path (default stdout)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	data := exportData{
		Global: store.GlobalLimits(),
		Tokens: store.List(),
	}

	var writer io.Writer = os.Stdout
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
		fmt.Printf("exported %d token(s) to %s\n", len(data.Tokens), *outFile)
	}
	return nil
}

// runImport imports tokens from a JSON file into the store.
func runImport(store *TokenStore, args []string) error {
	fs := flag.NewFlagSet("import", flag.ContinueOnError)
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

	if err := store.SetGlobalLimits(data.Global); err != nil {
		return fmt.Errorf("set global limits: %w", err)
	}

	imported := 0
	for _, rec := range data.Tokens {
		if rec.ID == "" {
			continue
		}
		if err := store.SetLimits(rec.ID, rec.Limits, rec.Bypass); err == nil {
			_ = store.SetDisabled(rec.ID, rec.Disabled)
			_ = store.SetInvites(rec.ID, rec.Invites)
			imported++
		}
	}

	fmt.Printf("imported global quota and updated %d token(s) from %s\n", imported, *inFile)
	return nil
}
