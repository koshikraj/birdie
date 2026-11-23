package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"text/tabwriter"

	"birdie/internal/bus"
	"birdie/internal/store"
)

const configUsage = "usage: birdie config list | get NAME | set NAME VALUE | unset NAME"

// configCmd reads and changes saved settings. A running server uses a change
// at once.
func configCmd(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("config", flag.ContinueOnError)
	db := fs.String("db", defaultDB(), "SQLite database path")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	st, err := store.Open(*db)
	if err != nil {
		return err
	}
	defer st.Close()
	return runConfig(ctx, os.Stdout, st, pos)
}

func runConfig(ctx context.Context, w io.Writer, st *store.SQLite, pos []string) error {
	if len(pos) == 0 || len(pos) == 1 && pos[0] == "list" {
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "SETTING\tVALUE\tMEANING")
		for _, s := range bus.Settings {
			v, saved, err := settingValue(ctx, st, s)
			if err != nil {
				return err
			}
			if !saved {
				v += " (default)"
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\n", s.Name, v, s.Help)
		}
		return tw.Flush()
	}
	want := map[string]int{"get": 2, "set": 3, "unset": 2}[pos[0]]
	if want == 0 || len(pos) != want {
		return errors.New(configUsage)
	}
	s, err := bus.FindSetting(pos[1])
	if err != nil {
		return fmt.Errorf("%w (birdie config list shows them all)", err)
	}
	switch pos[0] {
	case "get":
		v, _, err := settingValue(ctx, st, s)
		if err != nil {
			return err
		}
		fmt.Fprintln(w, v)
	case "set":
		if err := s.Check(pos[2]); err != nil {
			return err
		}
		if err := st.SetState(ctx, bus.SettingKey(s.Name), pos[2]); err != nil {
			return err
		}
		fmt.Fprintf(w, "%s is now %s.\n", s.Name, pos[2])
	case "unset":
		if err := st.DeleteState(ctx, bus.SettingKey(s.Name)); err != nil {
			return err
		}
		fmt.Fprintf(w, "%s is back to its default, %s.\n", s.Name, s.Default)
	}
	return nil
}

// settingValue is a setting's value and whether it was saved rather than the
// default.
func settingValue(ctx context.Context, st *store.SQLite, s bus.Setting) (string, bool, error) {
	v, err := st.GetState(ctx, bus.SettingKey(s.Name))
	if errors.Is(err, bus.ErrNotFound) {
		return s.Default, false, nil
	}
	return v, err == nil, err
}
