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
	"glenn.io/sigrets/internal/cfg"
	"glenn.io/sigrets/internal/crypto"
	"glenn.io/sigrets/internal/state"
	"glenn.io/sigrets/internal/store"
)

type screen int

const (
	screenLoading screen = iota
	screenBackends
	screenProjects
	screenStacks
	screenSecrets
	screenDetail
	screenError
)

type backendItem struct{ name string }

func (i backendItem) Title() string       { return i.name }
func (i backendItem) Description() string { return "" }
func (i backendItem) FilterValue() string { return i.name }

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

type loadBackendsMsg struct {
	backends []string
	err      error
}

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

type Model struct {
	ctx             context.Context
	s3              *store.S3Store
	layout          string
	screen          screen
	loadingMsg      string
	spinner         spinner.Model
	backends        list.Model
	projects        list.Model
	stacks          list.Model
	secrets         list.Model
	selectedBackend string
	selectedProject string
	selectedStack   string
	selectedSecret  *state.Secret
	plaintext       string
	revealed        bool
	copied          bool
	copiedCmd       bool
	decr            *crypto.Decryptor
	result          string
	err             error
	errMsg          string
	errBack         screen
	width           int
	height          int
	bucketSource    string
	profile         string
}

func New(ctx context.Context, s3 *store.S3Store, bucketSource, profile, layout string) Model {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = spinnerStyle

	backendList := list.New([]list.Item{}, list.NewDefaultDelegate(), 0, 0)
	backendList.Title = "Select a backend"
	backendList.Styles.Title = titleStyle

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
		layout:       layout,
		screen:       screenLoading,
		loadingMsg:   "Scanning bucket for backends…",
		spinner:      sp,
		backends:     backendList,
		projects:     projectList,
		stacks:       stackList,
		secrets:      secretList,
		bucketSource: bucketSource,
		profile:      profile,
	}
}

func (m Model) nested() bool { return m.layout == cfg.LayoutNested }

func (m Model) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, m.listBackendsCmd())
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		h, v := lipgloss.NewStyle().Margin(1, 2).GetFrameSize()
		m.width = msg.Width - h
		m.height = msg.Height - v
		m.backends.SetSize(m.width, m.height)
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
			if m.screen != screenDetail && m.screen != screenError {
				return m, tea.Quit
			}
		case "esc", "b":
			switch m.screen {
			case screenError:
				m.errMsg = ""
				m.screen = m.errBack
				return m, nil
			case screenProjects:
				m.screen = screenBackends
				return m, nil
			case screenStacks:
				if m.nested() {
					m.screen = screenProjects
				} else {
					m.screen = screenBackends
				}
				return m, nil
			case screenSecrets:
				m.screen = screenStacks
				return m, nil
			case screenDetail:
				m.screen = screenSecrets
				m.revealed = false
				m.copied = false
				m.copiedCmd = false
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
		case "c":
			if m.screen == screenDetail {
				s := m.selectedSecret
				sourceShort := "o"
				if s.Source == "config" {
					sourceShort = "c"
				}
				path := m.buildSecretPath(sourceShort, s.Name)
				cmd := "sigrets " + m.selectedBackend + " " + path
				err := clipboard.WriteAll(cmd)
				m.copiedCmd = err == nil
				return m, nil
			}
		case "enter":
			switch m.screen {
			case screenBackends:
				sel, ok := m.backends.SelectedItem().(backendItem)
				if !ok {
					return m, nil
				}
				m.selectedBackend = sel.name
				if m.nested() {
					m.loadingMsg = fmt.Sprintf("Loading projects for %s…", sel.name)
					m.screen = screenLoading
					return m, tea.Batch(m.spinner.Tick, m.loadProjectsCmd(sel.name))
				}
				m.loadingMsg = fmt.Sprintf("Loading stacks for %s…", sel.name)
				m.screen = screenLoading
				return m, tea.Batch(m.spinner.Tick, m.loadStacksCmd(""))

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
				m.copiedCmd = false
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

	case loadBackendsMsg:
		if msg.err != nil {
			m.err = msg.err
			return m, tea.Quit
		}
		if len(msg.backends) == 0 {
			m.err = fmt.Errorf("no backends found in bucket")
			return m, tea.Quit
		}
		items := make([]list.Item, len(msg.backends))
		for i, b := range msg.backends {
			items[i] = backendItem{b}
		}
		m.backends.SetItems(items)
		m.backends.SetSize(m.width, m.height)
		m.backends.Title = "Select a backend  " + dimStyle.Render("(bucket from: "+m.bucketSource+")")
		m.screen = screenBackends
		return m, nil

	case loadProjectsMsg:
		if msg.err != nil {
			m.errMsg = msg.err.Error()
			m.errBack = screenBackends
			m.screen = screenError
			return m, nil
		}
		if len(msg.projects) == 0 {
			m.errMsg = "no projects found in backend"
			m.errBack = screenBackends
			m.screen = screenError
			return m, nil
		}
		items := make([]list.Item, len(msg.projects))
		for i, p := range msg.projects {
			items[i] = projectItem{p}
		}
		m.projects.SetItems(items)
		m.projects.SetSize(m.width, m.height)
		m.projects.Title = "Select a project  " + dimStyle.Render("("+m.selectedBackend+")")
		m.screen = screenProjects
		return m, nil

	case loadStacksMsg:
		if msg.err != nil {
			if m.nested() {
				m.errBack = screenProjects
			} else {
				m.errBack = screenBackends
			}
			m.errMsg = msg.err.Error()
			m.screen = screenError
			return m, nil
		}
		if len(msg.stacks) == 0 {
			if m.nested() {
				m.errBack = screenProjects
			} else {
				m.errBack = screenBackends
			}
			m.errMsg = "no stacks found"
			m.screen = screenError
			return m, nil
		}
		items := make([]list.Item, len(msg.stacks))
		for i, s := range msg.stacks {
			items[i] = stackItem{s}
		}
		m.stacks.SetItems(items)
		m.stacks.SetSize(m.width, m.height)
		titleCtx := m.selectedBackend
		if m.nested() {
			titleCtx = m.selectedProject
		}
		m.stacks.Title = "Select a stack  " + dimStyle.Render("("+titleCtx+")")
		m.screen = screenStacks
		return m, nil

	case loadSecretsMsg:
		if msg.err != nil {
			m.errMsg = msg.err.Error()
			m.errBack = screenStacks
			m.screen = screenError
			return m, nil
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
	case screenBackends:
		var cmd tea.Cmd
		m.backends, cmd = m.backends.Update(msg)
		return m, cmd
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
	case screenError:
		card := lipgloss.JoinVertical(lipgloss.Left,
			errorLabelStyle.Render("error"),
			"",
			errorMsgStyle.Render(m.errMsg),
			"",
			helpStyle.Render("esc — go back • ctrl+c — quit"),
		)
		cardWidth := m.width - 4
		if cardWidth < 40 {
			cardWidth = 40
		}
		return wrap.Render(detailCardStyle.Width(cardWidth).Render(card))
	case screenBackends:
		return wrap.Render(m.backends.View() + "\n" + help)
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

func (m Model) buildSecretPath(sourceShort, secretName string) string {
	if m.nested() {
		return m.selectedProject + "." + m.selectedStack + "." + sourceShort + "." + secretName
	}
	return m.selectedStack + "." + sourceShort + "." + secretName
}

func (m Model) viewDetail() string {
	s := m.selectedSecret

	sourceShort := "o"
	if s.Source == "config" {
		sourceShort = "c"
	}
	path := m.buildSecretPath(sourceShort, s.Name)
	cmd := "sigrets " + m.selectedBackend + " " + path

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
	copiedCmdNote := ""
	if m.copiedCmd {
		copiedCmdNote = "  " + copiedStyle.Render("✓ copied")
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
		dimStyle.Render("y — copy value to clipboard"+copiedNote),
		dimStyle.Render("c — copy command to clipboard"+copiedCmdNote),
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

func (m Model) listBackendsCmd() tea.Cmd {
	return func() tea.Msg {
		backends, err := m.s3.ListBackends(m.ctx)
		return loadBackendsMsg{backends: backends, err: err}
	}
}

func (m Model) loadProjectsCmd(backend string) tea.Cmd {
	return func() tea.Msg {
		projects, err := m.s3.ListProjects(m.ctx, backend)
		return loadProjectsMsg{projects: projects, err: err}
	}
}

func (m Model) loadStacksCmd(project string) tea.Cmd {
	return func() tea.Msg {
		stacks, err := m.s3.ListStacks(m.ctx, m.selectedBackend, project)
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
			histCh <- histResult{m.s3.LatestHistoryKey(m.ctx, m.selectedBackend, m.selectedProject, info.Name)}
		}()

		sr := <-stateCh
		if sr.err != nil {
			return loadSecretsMsg{err: sr.err}
		}

		projectState, err := state.ParseProjectState(sr.data)
		if err != nil {
			return loadSecretsMsg{err: err}
		}

		secrets := state.ExtractOutputSecrets(projectState)

		if histKey := (<-histCh).key; histKey != "" {
			if histData, err := m.s3.ReadBytes(m.ctx, histKey); err == nil {
				if hist, err := state.ParseHistoryRecord(histData); err == nil {
					secrets = append(secrets, state.ExtractHistorySecrets(hist)...)
				}
			}
		}

		cloudState, err := state.ExtractCloudState(projectState)
		if err != nil {
			return loadSecretsMsg{err: err}
		}

		decr, err := crypto.NewDecryptor(m.ctx, cloudState.URL, cloudState.EncryptedKey, m.profile)
		if err != nil {
			return loadSecretsMsg{err: err}
		}

		return loadSecretsMsg{secrets: secrets, decr: decr}
	}
}
