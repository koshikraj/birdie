package bus

import (
	"context"
	"errors"
	"fmt"
	"strconv"
)

// SettingLoopLimit is how many messages one agent may send another in
// LoopWindow before they're refused as a loop. 0 turns the check off.
const SettingLoopLimit = "loop-limit"

// Setting is something `birdie config` can change while the bus runs.
type Setting struct {
	Name    string
	Help    string
	Default string
	// Check says what's wrong with a value, if anything.
	Check func(string) error
}

// Settings are all there are, in the order they're listed.
var Settings = []Setting{{
	Name:    SettingLoopLimit,
	Help:    "messages one agent may send another in 10 minutes before they're refused as a loop (0 = no limit)",
	Default: strconv.Itoa(DefaultLoopLimit),
	Check: func(v string) error {
		if n, err := strconv.Atoi(v); err != nil || n < 0 {
			return fmt.Errorf("%w: %s must be a whole number, 0 or more, not %q", ErrInvalid, SettingLoopLimit, v)
		}
		return nil
	},
}}

// FindSetting returns the setting called name.
func FindSetting(name string) (Setting, error) {
	for _, s := range Settings {
		if s.Name == name {
			return s, nil
		}
	}
	return Setting{}, fmt.Errorf("%w: no setting %q", ErrNotFound, name)
}

// SettingKey is where a setting's value is saved, as state.
func SettingKey(name string) string { return "setting:" + name }

// loopLimit is the limit in force: the one the bus was started with, else the
// saved one, else the default. Negative means no limit.
func (b *Bus) loopLimit(ctx context.Context) (int, error) {
	if b.LoopLimit != 0 {
		return b.LoopLimit, nil
	}
	v, err := b.store.GetState(ctx, SettingKey(SettingLoopLimit))
	if errors.Is(err, ErrNotFound) {
		return DefaultLoopLimit, nil
	} else if err != nil {
		return 0, err
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		b.log.Warn("ignoring a bad setting", "name", SettingLoopLimit, "value", v)
		return DefaultLoopLimit, nil
	}
	if n == 0 {
		return -1, nil
	}
	return n, nil
}
