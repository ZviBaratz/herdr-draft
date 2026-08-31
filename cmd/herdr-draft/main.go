package main

import (
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"
)

func version() string { return "0.1.0-dev" }

type smokeModel struct{}

func (m smokeModel) Init() tea.Cmd { return nil }

func (m smokeModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if k, ok := msg.(tea.KeyPressMsg); ok {
		switch k.String() {
		case "q", "esc", "ctrl+c":
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m smokeModel) View() tea.View {
	v := tea.NewView("herdr-draft " + version() + " — press q to close")
	v.AltScreen = true
	return v
}

func main() {
	if _, err := tea.NewProgram(smokeModel{}).Run(); err != nil {
		fmt.Fprintln(os.Stderr, "herdr-draft:", err)
		os.Exit(1)
	}
}
