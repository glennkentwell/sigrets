package tui

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"glenn.io/sigrets/internal/crypto"
	"glenn.io/sigrets/internal/state"
	"glenn.io/sigrets/internal/store"
)

type screen int

const (
	screenStacks screen = iota
	screenSecrets
)

type stackItem struct {
	info store.StackInfo
}

func (i stackItem) Title() string       { return i.info.Name }
func (i stackItem) Description() string { return i.info.StateKey }
func (i stackItem) FilterValue() string { return i.info.Name }

type secretItem struct {
	secret state.Secret
}

func (i secretItem) Title() string {
	label := labelOutputStyle.Render("output")
	if i.secret.Source == "config" {
		label = labelConfigStyle.Render("config")
	}
	return label + " " + i.secret.Name
}

func (i secretItem) Description() string { return dimStyle.Render("press enter to decrypt") }
func (i secretItem) FilterValue() string  { return i.secret.Name }

type loadSecretsMsg struct {
	secrets []state.Secret
	decr    *crypto.Decryptor
	err     error
}

type Model struct {
	ctx     context.Context
	s3      *store.S3Store
	screen  screen
	stacks  list.Model
	secrets list.Model
	decr    *crypto.Decryptor
	result  string
	err     error
}

func New(ctx context.Context, s3 *store.S3Store, stacks []store.StackInfo) Model {
	items := make([]list.Item, len(stacks))
	for i, s := range stacks {
		items[i] = stackItem{s}
	}

	stackList := list.New(items, list.NewDefaultDelegate(), 0, 0)
	stackList.Title = "Select a stack"
	stackList.Styles.Title = titleStyle

	secretList := list.New([]list.Item{}, list.NewDefaultDelegate(), 0, 0)
	secretList.Title = "Select a secret"
	secretList.Styles.Title = titleStyle

	return Model{
		ctx:     ctx,
		s3:      s3,
		screen:  screenStacks,
		stacks:  stackList,
		secrets: secretList,
	}
}

func (m Model) Init() tea.Cmd {
	return nil
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		h, v := lipgloss.NewStyle().Margin(1, 2).GetFrameSize()
		m.stacks.SetSize(msg.Width-h, msg.Height-v)
		m.secrets.SetSize(msg.Width-h, msg.Height-v)
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		case "esc", "b":
			if m.screen == screenSecrets {
				m.screen = screenStacks
				return m, nil
			}
		case "enter":
			if m.screen == screenStacks {
				selected, ok := m.stacks.SelectedItem().(stackItem)
				if !ok {
					return m, nil
				}
				return m, m.loadSecretsCmd(selected.info)
			}
			if m.screen == screenSecrets {
				selected, ok := m.secrets.SelectedItem().(secretItem)
				if !ok {
					return m, nil
				}
				if selected.secret.Ciphertext == "" {
					m.result = selected.secret.Value
					return m, tea.Quit
				}
				plaintext, err := m.decr.Decrypt(selected.secret.Ciphertext)
				if err != nil {
					m.err = err
					return m, tea.Quit
				}
				m.result = plaintext
				return m, tea.Quit
			}
		}

	case loadSecretsMsg:
		if msg.err != nil {
			m.err = msg.err
			return m, tea.Quit
		}
		m.decr = msg.decr
		items := make([]list.Item, len(msg.secrets))
		for i, s := range msg.secrets {
			items[i] = secretItem{s}
		}
		m.secrets.SetItems(items)
		m.screen = screenSecrets
		return m, nil
	}

	if m.screen == screenStacks {
		var cmd tea.Cmd
		m.stacks, cmd = m.stacks.Update(msg)
		return m, cmd
	}
	var cmd tea.Cmd
	m.secrets, cmd = m.secrets.Update(msg)
	return m, cmd
}

func (m Model) View() string {
	if m.err != nil {
		return fmt.Sprintf("error: %v\n", m.err)
	}
	help := helpStyle.Render("↑/↓ navigate • enter select • esc back • q quit")
	if m.screen == screenStacks {
		return lipgloss.NewStyle().Margin(1, 2).Render(m.stacks.View() + "\n" + help)
	}
	return lipgloss.NewStyle().Margin(1, 2).Render(m.secrets.View() + "\n" + help)
}

func (m Model) Result() string { return m.result }
func (m Model) Err() error     { return m.err }

func (m Model) loadSecretsCmd(info store.StackInfo) tea.Cmd {
	return func() tea.Msg {
		stateData, err := m.s3.ReadState(m.ctx, info.StateKey)
		if err != nil {
			return loadSecretsMsg{err: err}
		}

		stackState, err := state.ParseStackState(stateData)
		if err != nil {
			return loadSecretsMsg{err: err}
		}

		secrets := state.ExtractOutputSecrets(stackState)

		if m.s3.HasConfig(m.ctx, info.ConfigKey) {
			if cfgData, err := m.s3.ReadConfig(m.ctx, info.ConfigKey); err == nil {
				if cfg, err := state.ParseStackConfig(cfgData); err == nil {
					secrets = append(secrets, state.ExtractConfigSecrets(cfg)...)
				}
			}
		}

		cloudState, err := extractCloudState(stackState)
		if err != nil {
			return loadSecretsMsg{err: err}
		}

		decr, err := crypto.NewDecryptor(m.ctx, cloudState.URL, cloudState.EncryptedKey)
		if err != nil {
			return loadSecretsMsg{err: err}
		}

		return loadSecretsMsg{secrets: secrets, decr: decr}
	}
}

func extractCloudState(stackState *state.StackState) (state.CloudSecretsState, error) {
	sp := stackState.Deployment.SecretsProviders
	if sp == nil {
		return state.CloudSecretsState{}, fmt.Errorf("stack has no secrets provider")
	}
	if sp.Type != "cloud" {
		return state.CloudSecretsState{}, fmt.Errorf("unsupported secrets provider %q (only \"cloud\"/KMS supported)", sp.Type)
	}
	var cs state.CloudSecretsState
	if err := json.Unmarshal(sp.State, &cs); err != nil {
		return state.CloudSecretsState{}, fmt.Errorf("parsing cloud secrets state: %w", err)
	}
	return cs, nil
}
