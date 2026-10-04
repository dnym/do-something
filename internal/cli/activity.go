package cli

import (
	"bufio"
	"dosomething/internal/i18n"
	"dosomething/internal/model"
	"dosomething/internal/output"
	"dosomething/internal/store"
	"fmt"
	"io"
	"strings"
)

func logActivity(s *store.Store, f LogFlags, interactive bool, in io.Reader, prompt io.Writer, tr *i18n.Translator) (output.LogMutation, error) {
	result := output.LogMutation{SchemaVersion: output.SchemaVersion}
	id, err := s.ResolveItemRef(f.ID)
	if err != nil {
		return result, err
	}
	it, err := s.GetItem(id)
	if err != nil {
		return result, err
	}
	activity, err := store.PrepareLog(it, f.Duration, f.Cost, f.Force)
	if err != nil {
		return result, err
	}
	if f.Complete && it.Ongoing {
		return result, &store.LogError{Reason: "ongoing_complete"}
	}
	if f.Complete && activity.Duration == nil {
		return result, &store.LogError{Reason: "complete_without_time"}
	}
	if f.Complete && (it.Status == model.StatusDone || it.Status == model.StatusDropped) {
		return result, &store.LogError{Reason: "complete_inactive"}
	}
	complete := f.Complete
	if it.Status == model.StatusInProgress && activity.RemainingAfter != nil && *activity.RemainingAfter == 0 && interactive && !f.Complete && !f.NoComplete {
		complete, err = askLogCompletion(in, prompt, tr)
		if err != nil {
			return result, err
		}
	}
	result.Log, err = s.LogTime(id, f.Duration, f.Cost, complete, f.Force)
	result.Changed = err == nil
	return result, err
}

func askLogCompletion(in io.Reader, out io.Writer, tr *i18n.Translator) (bool, error) {
	reader := bufio.NewReader(in)
	for {
		if _, err := fmt.Fprint(out, tr.T("prompt.log_complete")+" "); err != nil {
			return false, err
		}
		line, err := reader.ReadString('\n')
		if err != nil {
			return false, err
		}
		switch strings.ToLower(strings.TrimSpace(line)) {
		case "y", "yes", "j", "ja":
			return true, nil
		case "", "n", "no", "nej":
			return false, nil
		}
		if _, err := fmt.Fprintln(out, tr.T("prompt.yes_no")); err != nil {
			return false, err
		}
	}
}
