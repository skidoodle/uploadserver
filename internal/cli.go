package internal

import (
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"text/tabwriter"
	"time"
)

const CLIUsage = `usage: uploadserver <command> [<args>]

Commands:
  run                        Start the web server
  list                       List all tokens, with usage and quotas
  add [--label L] [--role R] Create a new token
  rm <id>                    Delete a token
  disable <id>               Disable a token
  enable <id>                Enable a token
  limit <id> [flags]         Set upload quotas for a token (use --help for flags)
  global [flags]             Show or set the server-wide default quota
  dump                       Decode the binary store and print everything in it
  reset                      Delete all tokens and reset store`

// RunTokenCLI handles the CLI subcommands, operating directly on the on-disk store.
func RunTokenCLI(args []string) (err error) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, CLIUsage)
		return errors.New("no subcommand given")
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
