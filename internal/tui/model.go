package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/atotto/clipboard"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"glenn.io/sigrets/internal/crypto"
	"glenn.io/sigrets/internal/state"
	"glenn.io/sigrets/internal/store"
)

type screen int

const (
	screenLoading screen = iota
	screenProjects
	screenStacks
	screenSecrets
	screenDetail
)

type projectItem struct{ name string }

func (i projectItem) Title() string       { return i.name }
func (i projectItem) Description() string { return "" }
func (i projectItem) FilterValue() string { return i.name }

type stackItem struct{ info store.StackInfo }

func (i stackItem) Title() string       { return i.info.Name }
func (i stackItem) Description() string { return dimStyle.Render(i.info.StateKey) }
func (i stackItem) FilterValue() string { return i.info.Name }

type secretItem struct{ secret state.Secret }

func (i secretItem) Title() string {
	label := labelOutputStyle.Render("output")
	if i.secret.Source == "config" {
		label = labelConfigStyle.Render("config")
	}
	return label + " " + i.secret.Name
}
func (i secretItem) Description() string { return dimStyle.Render("press enter to view") }
func (i secretItem) FilterValue() string  { return i.secret.Name }

type loadProjectsMsg struct {
	projects []string
	err      error
}

type loadStacksMsg struct {
	stacks []store.StackInfo
	err    error
}

type loadSecretsMsg struct {
	secrets []state.Secret
	decr    *crypto.Decryptor
	err     error
}

type decryptedMsg struct {
	plaintext string
	err       error
}

type clipboardMsg struct{ err error }

type Model struct {
	ctx             context.Context
	s3              *store.S3Store
	screen          screen
	loadingMsg      string
	spinner         spinner.Model
	projects        list.Model
	stacks          list.Model
	secrets         list.Model
	selectedProject string
	selectedStack   string
	selectedSecret  *state.Secret
	plaintext       string
	revealed        bool
	copied          bool
	decr            *crypto.Decryptor
	result          string
	err             error
	width           int
	height          int
	bucketSource    string
}

func New(ctx context.Context, s3 *store.S3Store, bucketSource string) Model {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = spinnerStyle

	projectList := list.New([]list.Item{}, list.NewDefaultDelegate(), 0, 0)
	projectList.Title = "Select a project"
	projectList.Styles.Title = titleStyle

	stackList := list.New([]list.Item{}, list.NewDefaultDelegate(), 0, 0)
	stackList.Title = "Select a stack"
	stackList.Styles.Title = titleStyle

	secretList := list.New([]list.Item{}, list.NewDefaultDelegate(), 0, 0)
	secretList.Title = "Select a secret"
	secretList.Styles.Title = titleStyle

	return Model{
		ctx:          ctx,
		s3:           s3,
		screen:       screenLoading,
		loadingMsg:   "Scanning bucket for projects…",
		spinner:      sp,
		projects:     projectList,
		stacks:       stackList,
		secrets:      secretList,
		bucketSource: bucketSource,
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, m.listProjectsCmd())
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		h, v := lipgloss.NewStyle().Margin(1, 2).GetFrameSize()
		m.width = msg.Width - h
		m.height = msg.Height - v
		m.projects.SetSize(m.width, m.height)
		m.stacks.SetSize(m.width, m.height)
		m.secrets.SetSize(m.width, m.height)
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		case "q":
			if m.screen != screenDetail {
				return m, tea.Quit
			}
		case "esc", "b":
			switch m.screen {
			case screenStacks:
				m.screen = screenProjects
				return m, nil
			case screenSecrets:
				m.screen = screenStacks
				return m, nil
			case screenDetail:
				m.screen = screenSecrets
				m.revealed = false
				m.copied = false
				m.plaintext = ""
				m.selectedSecret = nil
				return m, nil
			}
		case " ", "v":
			if m.screen == screenDetail {
				m.revealed = !m.revealed
				return m, nil
			}
		case "y":
			if m.screen == screenDetail && m.plaintext != "" {
				err := clipboard.WriteAll(m.plaintext)
				m.copied = err == nil
				return m, nil
			}
		case "enter":
			switch m.screen {
			case screenProjects:
				sel, ok := m.projects.SelectedItem().(projectItem)
				if !ok {
					return m, nil
				}
				m.selectedProject = sel.name
				m.loadingMsg = fmt.Sprintf("Loading stacks for %s…", sel.name)
				m.screen = screenLoading
				return m, tea.Batch(m.spinner.Tick, m.loadStacksCmd(sel.name))

			case screenStacks:
				sel, ok := m.stacks.SelectedItem().(stackItem)
				if !ok {
					return m, nil
				}
				m.selectedStack = sel.info.Name
				m.loadingMsg = fmt.Sprintf("Decrypting secrets for %s…", sel.info.Name)
				m.screen = screenLoading
				return m, tea.Batch(m.spinner.Tick, m.loadSecretsCmd(sel.info))

			case screenSecrets:
				sel, ok := m.secrets.SelectedItem().(secretItem)
				if !ok {
					return m, nil
				}
				s := sel.secret
				m.selectedSecret = &s
				m.revealed = false
				m.copied = false
				if s.Ciphertext == "" {
					m.plaintext = s.Value
				} else {
					plaintext, err := m.decr.Decrypt(s.Ciphertext)
					if err != nil {
						m.err = err
						return m, tea.Quit
					}
					m.plaintext = plaintext
				}
				m.screen = screenDetail
				return m, nil

			case screenDetail:
				m.result = m.plaintext
				return m, tea.Quit
			}
		}

	case loadProjectsMsg:
		if msg.err != nil {
			m.err = msg.err
			return m, tea.Quit
		}
		if len(msg.projects) == 0 {
			m.err = fmt.Errorf("no projects found in bucket")
			return m, tea.Quit
		}
		items := make([]list.Item, len(msg.projects))
		for i, p := range msg.projects {
			items[i] = projectItem{p}
		}
		m.projects.SetItems(items)
		m.projects.SetSize(m.width, m.height)
		m.projects.Title = "Select a project  " + dimStyle.Render("(bucket from: "+m.bucketSource+")")
		m.screen = screenProjects
		return m, nil

	case loadStacksMsg:
		if msg.err != nil {
			m.err = msg.err
			return m, tea.Quit
		}
		items := make([]list.Item, len(msg.stacks))
		for i, s := range msg.stacks {
			items[i] = stackItem{s}
		}
		m.stacks.SetItems(items)
		m.stacks.SetSize(m.width, m.height)
		m.stacks.Title = "Select a stack  " + dimStyle.Render("("+m.selectedProject+")")
		m.screen = screenStacks
		return m, nil

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
		m.secrets.SetSize(m.width, m.height)
		m.secrets.Title = "Select a secret  " + dimStyle.Render("("+m.selectedStack+")")
		m.screen = screenSecrets
		return m, nil
	}

	switch m.screen {
	case screenProjects:
		var cmd tea.Cmd
		m.projects, cmd = m.projects.Update(msg)
		return m, cmd
	case screenStacks:
		var cmd tea.Cmd
		m.stacks, cmd = m.stacks.Update(msg)
		return m, cmd
	case screenSecrets:
		var cmd tea.Cmd
		m.secrets, cmd = m.secrets.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m Model) View() string {
	if m.err != nil {
		return fmt.Sprintf("\n  error: %v\n", m.err)
	}
	wrap := lipgloss.NewStyle().Margin(1, 2)
	help := helpStyle.Render("↑/↓ navigate • enter select • esc back • q quit")

	switch m.screen {
	case screenLoading:
		return wrap.Render(m.spinner.View() + " " + loadingStyle.Render(m.loadingMsg))
	case screenProjects:
		return wrap.Render(m.projects.View() + "\n" + help)
	case screenStacks:
		return wrap.Render(m.stacks.View() + "\n" + help)
	case screenSecrets:
		return wrap.Render(m.secrets.View() + "\n" + help)
	case screenDetail:
		return wrap.Render(m.viewDetail())
	}
	return ""
}

func (m Model) viewDetail() string {
	s := m.selectedSecret

	sourceShort := "o"
	if s.Source == "config" {
		sourceShort = "c"
	}
	path := m.selectedStack + "." + sourceShort + "." + s.Name
	cmd := "sigrets " + m.selectedProject + " " + path

	valueWidth := m.width - 8
	if valueWidth < 20 {
		valueWidth = 20
	}

	var valueRow string
	if m.revealed {
		wrapped := wrapString(m.plaintext, valueWidth)
		valueRow = revealedValueStyle.Render(wrapped)
	} else {
		dots := strings.Repeat("●", min(len(m.plaintext), 32))
		valueRow = hiddenValueStyle.Render(dots)
	}

	eyeHint := "👁  space/v — reveal"
	if m.revealed {
		eyeHint = "👁  space/v — hide"
	}

	copiedNote := ""
	if m.copied {
		copiedNote = "  " + copiedStyle.Render("✓ copied")
	}

	card := lipgloss.JoinVertical(lipgloss.Left,
		detailLabelStyle.Render("path"),
		pathStyle.Render(path),
		"",
		detailLabelStyle.Render("command"),
		pathStyle.Render(cmd),
		"",
		detailLabelStyle.Render("value"),
		valueRow,
		"",
		dimStyle.Render(eyeHint),
		dimStyle.Render("y — copy to clipboard"+copiedNote),
		dimStyle.Render("enter — print & exit"),
		dimStyle.Render("esc — back"),
	)

	cardWidth := m.width - 4
	if cardWidth < 40 {
		cardWidth = 40
	}

	return detailCardStyle.Width(cardWidth).Render(card)
}

func (m Model) Result() string { return m.result }
func (m Model) Err() error     { return m.err }

func wrapString(s string, width int) string {
	if len(s) <= width {
		return s
	}
	var b strings.Builder
	for len(s) > width {
		b.WriteString(s[:width])
		b.WriteByte('\n')
		s = s[width:]
	}
	b.WriteString(s)
	return b.String()
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (m Model) listProjectsCmd() tea.Cmd {
	return func() tea.Msg {
		projects, err := m.s3.ListProjects(m.ctx)
		return loadProjectsMsg{projects: projects, err: err}
	}
}

func (m Model) loadStacksCmd(project string) tea.Cmd {
	return func() tea.Msg {
		stacks, err := m.s3.ListStacks(m.ctx, project)
		return loadStacksMsg{stacks: stacks, err: err}
	}
}

func (m Model) loadSecretsCmd(info store.StackInfo) tea.Cmd {
	return func() tea.Msg {
		type stateResult struct {
			data []byte
			err  error
		}
		type histResult struct {
			key string
		}

		stateCh := make(chan stateResult, 1)
		histCh := make(chan histResult, 1)

		go func() {
			data, err := m.s3.ReadState(m.ctx, info.StateKey)
			stateCh <- stateResult{data, err}
		}()
		go func() {
			histCh <- histResult{m.s3.LatestHistoryKey(m.ctx, m.selectedProject, info.Name)}
		}()

		sr := <-stateCh
		if sr.err != nil {
			return loadSecretsMsg{err: sr.err}
		}

		stackState, err := state.ParseStackState(sr.data)
		if err != nil {
			return loadSecretsMsg{err: err}
		}

		secrets := state.ExtractOutputSecrets(stackState)

		if histKey := (<-histCh).key; histKey != "" {
			if histData, err := m.s3.ReadBytes(m.ctx, histKey); err == nil {
				if hist, err := state.ParseHistoryRecord(histData); err == nil {
					secrets = append(secrets, state.ExtractHistorySecrets(hist)...)
				}
			}
		}

		cloudState, err := state.ExtractCloudState(stackState)
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


