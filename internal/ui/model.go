package ui

import (
	"clirss/internal/db"
	"clirss/internal/rss"
	"fmt"
	"os/exec"
	"runtime"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
)

type state int

const (
	stateFeeds state = iota
	stateEntries
	stateReading
	stateAddingFeed
)

type errMsg error

type feedItem struct {
	feed db.Feed
}

func (i feedItem) Title() string       { return i.feed.Title }
func (i feedItem) Description() string { return i.feed.URL }
func (i feedItem) FilterValue() string { return i.feed.Title }

type entryItem struct {
	entry db.Entry
}

func (i entryItem) Title() string {
	if i.entry.Read {
		return "  " + i.entry.Title
	}
	return "● " + i.entry.Title
}
func (i entryItem) Description() string { return i.entry.PublishedAt.Format("2006-01-02 15:04") }
func (i entryItem) FilterValue() string { return i.entry.Title }

type Model struct {
	state       state
	feedsList   list.Model
	entriesList list.Model
	viewport    viewport.Model
	textInput   textinput.Model
	spinner     spinner.Model
	loading     bool
	err         error
	width       int
	height      int
	currentFeed db.Feed
	renderer    *glamour.TermRenderer
}

func NewModel() Model {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))

	ti := textinput.New()
	ti.Placeholder = "RSS Feed URL"
	ti.Focus()

	m := Model{
		state:       stateFeeds,
		feedsList:   list.New([]list.Item{}, list.NewDefaultDelegate(), 0, 0),
		entriesList: list.New([]list.Item{}, list.NewDefaultDelegate(), 0, 0),
		viewport:    viewport.New(0, 0),
		textInput:   ti,
		spinner:     s,
		loading:     true,
	}
	m.feedsList.Title = "Feeds"
	m.feedsList.SetShowStatusBar(false)

	renderer, _ := glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(80), // Default width
	)
	m.renderer = renderer

	return m
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(m.loadFeeds, m.spinner.Tick)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.feedsList.SetSize(msg.Width-4, msg.Height-4)
		m.entriesList.SetSize(msg.Width-4, msg.Height-4)
		m.viewport = viewport.New(msg.Width-4, msg.Height-4)
		m.textInput.Width = msg.Width - 10

		renderer, _ := glamour.NewTermRenderer(
			glamour.WithAutoStyle(),
			glamour.WithWordWrap(msg.Width-10),
		)
		m.renderer = renderer

	case tea.KeyMsg:
		switch m.state {
		case stateFeeds:
			switch msg.String() {
			case "a":
				m.state = stateAddingFeed
				m.textInput.Focus()
				return m, nil
			case "r":
				m.loading = true
				return m, m.refreshAllFeeds
			case "d":
				if i, ok := m.feedsList.SelectedItem().(feedItem); ok {
					m.loading = true
					return m, m.deleteFeed(i.feed.ID)
				}
			case "enter":
				if i, ok := m.feedsList.SelectedItem().(feedItem); ok {
					m.currentFeed = i.feed
					m.state = stateEntries
					m.loading = true
					m.entriesList.SetItems([]list.Item{}) // Clear previous entries
					return m, m.loadEntries(i.feed.ID)
				}
			case "q":
				return m, tea.Quit
			}

		case stateEntries:
			switch msg.String() {
			case "esc", "backspace":
				m.state = stateFeeds
				return m, nil
			case "r":
				m.loading = true
				return m, m.refreshCurrentFeed
			case "enter":
				if i, ok := m.entriesList.SelectedItem().(entryItem); ok {
					m.state = stateReading
					m.loading = true
					m.viewport.SetContent("Loading content...") // Clear previous content
					return m, m.viewEntry(i.entry)
				}
			case "b":
				if i, ok := m.entriesList.SelectedItem().(entryItem); ok {
					openBrowser(i.entry.Link)
				}
			}

		case stateReading:
			switch msg.String() {
			case "esc", "backspace":
				m.state = stateEntries
				return m, nil
			case "b":
				if i, ok := m.entriesList.SelectedItem().(entryItem); ok {
					openBrowser(i.entry.Link)
				}
			}

		case stateAddingFeed:
			switch msg.String() {
			case "esc":
				m.state = stateFeeds
				m.textInput.Reset()
				return m, nil
			case "enter":
				url := m.textInput.Value()
				m.state = stateFeeds
				m.textInput.Reset()
				m.loading = true
				return m, m.addFeed(url)
			}
		}

	case feedsMsg:
		m.feedsList.SetItems(msg.items)
		m.loading = false

	case entriesMsg:
		m.entriesList.SetItems(msg.items)
		m.entriesList.Title = m.currentFeed.Title
		m.loading = false

	case contentMsg:
		m.viewport.SetContent(string(msg))
		m.loading = false

	case errMsg:
		m.err = msg
		m.loading = false

	case spinner.TickMsg:
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	}

	// Route updates to sub-models
	switch m.state {
	case stateFeeds:
		m.feedsList, cmd = m.feedsList.Update(msg)
		cmds = append(cmds, cmd)
	case stateEntries:
		m.entriesList, cmd = m.entriesList.Update(msg)
		cmds = append(cmds, cmd)
	case stateReading:
		m.viewport, cmd = m.viewport.Update(msg)
		cmds = append(cmds, cmd)
	case stateAddingFeed:
		m.textInput, cmd = m.textInput.Update(msg)
		cmds = append(cmds, cmd)
	}

	return m, tea.Batch(cmds...)
}

func (m Model) View() string {
	if m.err != nil {
		return ErrorStyle.Render(fmt.Sprintf("Error: %v", m.err))
	}

	header := ""
	if m.loading {
		header = m.spinner.View() + " Loading..."
	}

	var content string
	switch m.state {
	case stateFeeds:
		content = m.feedsList.View()
	case stateEntries:
		content = m.entriesList.View()
	case stateReading:
		content = TitleStyle.Render(m.entriesList.SelectedItem().(entryItem).entry.Title) + "\n\n" + m.viewport.View()
	case stateAddingFeed:
		content = "Add Feed URL:\n\n" + m.textInput.View() + "\n\n(esc to cancel)"
	}

	return DocStyle.Render(header + "\n" + content)
}

// Commands
type feedsMsg struct{ items []list.Item }
type entriesMsg struct{ items []list.Item }
type contentMsg string

func (m Model) loadFeeds() tea.Msg {
	feeds, err := db.GetFeeds()
	if err != nil {
		return errMsg(err)
	}
	items := make([]list.Item, len(feeds))
	for i, f := range feeds {
		items[i] = feedItem{feed: f}
	}
	return feedsMsg{items}
}

func (m Model) loadEntries(feedID int64) tea.Cmd {
	return func() tea.Msg {
		entries, err := db.GetEntries(feedID)
		if err != nil {
			return errMsg(err)
		}
		items := make([]list.Item, len(entries))
		for i, e := range entries {
			items[i] = entryItem{entry: e}
		}
		return entriesMsg{items}
	}
}

func (m Model) addFeed(url string) tea.Cmd {
	return func() tea.Msg {
		f, err := rss.FetchFeed(url)
		if err != nil {
			return errMsg(err)
		}
		id, err := db.AddFeed(url, f.Title, f.Description)
		if err != nil {
			return errMsg(err)
		}
		err = rss.SyncFeed(id, url)
		if err != nil {
			return errMsg(err)
		}
		return m.loadFeeds()
	}
}

func (m Model) deleteFeed(id int64) tea.Cmd {
	return func() tea.Msg {
		err := db.DeleteFeed(id)
		if err != nil {
			return errMsg(err)
		}
		return m.loadFeeds()
	}
}

func (m Model) refreshAllFeeds() tea.Msg {
	feeds, _ := db.GetFeeds()
	for _, f := range feeds {
		rss.SyncFeed(f.ID, f.URL)
	}
	return m.loadFeeds()
}

func (m Model) refreshCurrentFeed() tea.Msg {
	rss.SyncFeed(m.currentFeed.ID, m.currentFeed.URL)
	return m.loadEntries(m.currentFeed.ID)()
}

func (m Model) viewEntry(e db.Entry) tea.Cmd {
	return func() tea.Msg {
		db.MarkAsRead(e.ID)
		out, _ := m.renderer.Render(e.Content)
		if out == "" || out == "\n" {
			out, _ = m.renderer.Render(e.Description)
		}
		return contentMsg(out)
	}
}

func openBrowser(url string) {
	var err error
	switch runtime.GOOS {
	case "linux":
		err = exec.Command("xdg-open", url).Start()
	case "windows":
		err = exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	case "darwin":
		err = exec.Command("open", url).Start()
	}
	_ = err
}

