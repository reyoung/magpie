package tui

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/yetone/magpie/internal/agent"
	"github.com/yetone/magpie/internal/provider"
)

// Startup's list arrives after the picker was opened: the real Cursor
// provider must replace Auto without another keypress or a manual fetch.
func TestTUIFetchesSubscriptionModels(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake cursor-agent is a shell script")
	}
	for _, view := range []string{"routing", "routing with catalog sync", "routing with catalog sync last", "provider", "classifier", "agent"} {
		t.Run(view, func(t *testing.T) {
			h := home(t)
			cli := filepath.Join(h, "cursor-agent")
			write(t, cli, `#!/bin/sh
case "$1" in
about) echo '{"userEmail":"cursor@example.com","subscriptionTier":"pro"}' ;;
models) printf 'Available models\n\nauto - Auto (default)\ncomposer-2.5 - Composer 2.5\ngpt-5.2 - GPT-5.2\ngpt-5.5 - GPT-5.5\n' ;;
*) exit 1 ;;
esac
`)
			if err := os.Chmod(cli, 0o755); err != nil {
				t.Fatal(err)
			}
			old := provider.CursorExecutable
			provider.CursorExecutable = func() string { return cli }
			provider.ForgetAccounts()
			t.Cleanup(func() {
				provider.CursorExecutable = old
				provider.ForgetAccounts()
			})
			// A cold models.dev sync must neither replace nor suppress the
			// subscription fetch. No test request leaves this process.
			client := http.DefaultClient
			http.DefaultClient = &http.Client{Transport: modelsTransport{}}
			t.Cleanup(func() { http.DefaultClient = client })

			m := model{w: 120, h: 40, syncing: strings.Contains(view, "sync")}
			init := m.Init()
			var want string
			switch view {
			case "provider":
				m.page = pageProviders
				m.reloadProviders()
				for i, p := range m.provs {
					if p.ID == "cursor" {
						m.prow = i
					}
				}
				m.openProviderModels("cursor")
				want = "composer-2.5"
			case "classifier":
				m.page = pageGroups
				m.openClassifier(provider.Group{ID: "test", Name: "Test"})
				want = "cursor/composer-2.5"
			case "agent":
				a, err := agent.Find("codex")
				if err != nil {
					t.Fatal(err)
				}
				m.agents = []*agent.Agent{a}
				m.reload()
				m.openFieldPicker()
				want = "cursor/composer-2.5"
			default:
				m.page = pageGroups
				m.openMemberPicker(pickMsg{newGroup: "Test"})
				want = "cursor/composer-2.5"
			}
			m = typeIn(m, "auto")
			// GPT-5.5 is also served by b, so the fetched list adds an
			// auto-group that sorts before cursor/auto under this filter.
			// Keep the selected model rather than its old row number.
			selected, _ := m.pk.choice()
			if selected == "" {
				t.Fatal("Cursor's fallback Auto is not in the picker")
			}
			m = runModelsInit(m, init, strings.HasSuffix(view, "last"))
			if got, _ := m.pk.choice(); got != selected || m.pk.input.Value() != "auto" || m.mode != modePick {
				t.Fatalf("fetch changed the open picker: choice %q, filter %q, mode %v", got, m.pk.input.Value(), m.mode)
			}
			m.pk.input.SetValue("composer-2.5")
			m.pk.refilter()
			if got, _ := m.pk.choice(); got != want {
				t.Fatalf("startup left Cursor's picker on Auto alone: got %q, want %q\n%s", got, want, m.View())
			}
		})
	}
}

// Bubble Tea executes each batched command and delivers its result. Hold
// them until the picker is open to reproduce a late vendor answer exactly.
func runModelsInit(m model, cmd tea.Cmd, reverse bool) model {
	if cmd == nil {
		return m
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for i := range batch {
			j := i
			if reverse {
				j = len(batch) - 1 - i
			}
			m = runModelsInit(m, batch[j], reverse)
		}
		return m
	}
	next, _ := m.Update(msg)
	return next.(model)
}

type modelsTransport struct{}

func (modelsTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.String() != "https://models.dev/api.json" {
		return nil, fmt.Errorf("unexpected request to %s", r.URL.Host)
	}
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"openai":{"id":"openai","models":{}}}`)), Request: r}, nil
}
