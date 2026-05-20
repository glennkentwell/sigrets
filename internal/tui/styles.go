package tui

import "github.com/charmbracelet/lipgloss"

var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("205")).
			MarginBottom(1)

	selectedStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("212")).
			Bold(true)

	labelOutputStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("39")).
				Width(8)

	labelConfigStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("214")).
				Width(8)

	dimStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("240"))

	helpStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("241")).
			MarginTop(1)

	spinnerStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("205"))

	loadingStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("252"))

	detailCardStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("238")).
			Padding(1, 2)

	detailLabelStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("240")).
				Bold(true)

	pathStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("39")).
			Bold(true)

	revealedValueStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("212"))

	hiddenValueStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("240"))

	copiedStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("82"))

	errorLabelStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("196")).
				Bold(true)

	errorMsgStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("252"))
)
