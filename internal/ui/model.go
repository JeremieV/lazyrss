package ui

import (
	"clirss/internal/db"
	"clirss/internal/rss"
	"fmt"
	"os/exec"
	"runtime"
	"time"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	"github.com/charmbracelet/bubbles/key"
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
	stateHelp
)

type errMsg error

type feedItem struct {
	feed db.Feed
}

func (i feedItem) Title() string {
	if i.feed.UnreadCount > 0 {
		return fmt.Sprintf("%s (%d)", i.feed.Title, i.feed.UnreadCount)
	}
	return i.feed.Title
}
func (i feedItem) Description() string { return i.feed.URL }
func (i feedItem) FilterValue() string { return i.feed.Title }

type entryItem struct {
	entry          db.Entry
	feedLastReadAt time.Time
}

func (i entryItem) Title() string {
	if i.entry.PublishedAt.After(i.feedLastReadAt) {
		return UnreadItemStyle.Render(i.entry.Title)
	}
	return i.entry.Title
}
func (i entryItem) Description() string { return i.entry.PublishedAt.Format("2006-01-02 15:04") }
func (i entryItem) FilterValue() string { return i.entry.Title }

type Model struct {
	state         state
	previousState state
	feedsList     list.Model
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
		loading:     true, // Set to true initially so the user sees the spinner immediately
	}
	m.feedsList.Title = "Feeds"
	m.feedsList.SetShowStatusBar(false)
	m.feedsList.AdditionalFullHelpKeys = func() []key.Binding {
		return []key.Binding{
			key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
		}
	}
	m.entriesList.AdditionalFullHelpKeys = m.feedsList.AdditionalFullHelpKeys

	return m
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(
		m.loadFeeds,
		m.spinner.Tick,
		m.refreshAllFeeds, // Start background sync directly
	)
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
		m.viewport.Width = msg.Width - 4
		m.viewport.Height = msg.Height - 4
		m.textInput.Width = msg.Width - 10

		// Update renderer with new width, but don't block if it's identical
		if m.renderer == nil || m.width != msg.Width {
			r, _ := glamour.NewTermRenderer(
				glamour.WithAutoStyle(),
				glamour.WithWordWrap(msg.Width-10),
			)
			m.renderer = r
		}

	case tea.KeyMsg:
		if msg.String() == "?" && m.state != stateHelp && m.state != stateAddingFeed {
			m.previousState = m.state
			m.state = stateHelp
			return m, nil
		}

		switch m.state {
		case stateHelp:
			if msg.String() == "q" || msg.String() == "esc" || msg.String() == "backspace" || msg.String() == "?" {
				m.state = m.previousState
				return m, nil
			}

		case stateFeeds:
			switch msg.String() {
			case "alt+up", "alt+k":
				idx := m.feedsList.Index()
				if idx > 0 {
					itemA := m.feedsList.Items()[idx].(feedItem)
					itemB := m.feedsList.Items()[idx-1].(feedItem)
					// If they have the same position, ensure they are different before swapping
					posA, posB := itemA.feed.Position, itemB.feed.Position
					if posA == posB {
						posA = idx
						posB = idx - 1
					}
					db.SwapFeedPositions(int(itemA.feed.ID), posA, int(itemB.feed.ID), posB)
					return m, m.loadFeedsWithIndex(idx - 1)
				}
			case "alt+down", "alt+j":
				idx := m.feedsList.Index()
				if idx < len(m.feedsList.Items())-1 {
					itemA := m.feedsList.Items()[idx].(feedItem)
					itemB := m.feedsList.Items()[idx+1].(feedItem)
					posA, posB := itemA.feed.Position, itemB.feed.Position
					if posA == posB {
						posA = idx
						posB = idx + 1
					}
					db.SwapFeedPositions(int(itemA.feed.ID), posA, int(itemB.feed.ID), posB)
					return m, m.loadFeedsWithIndex(idx + 1)
				}
			case "left":
				m.feedsList.CursorUp()
				return m, nil
			case "right":
				m.feedsList.CursorDown()
				return m, nil
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
					return m, m.loadEntries(i.feed)
				}
			case "q":
				return m, tea.Quit
			}

		case stateEntries:
			switch msg.String() {
			case "left":
				m.feedsList.CursorUp()
				if i, ok := m.feedsList.SelectedItem().(feedItem); ok {
					m.currentFeed = i.feed
					m.state = stateEntries
					m.loading = true
					m.entriesList.SetItems([]list.Item{})
					return m, m.loadEntries(i.feed)
				}
			case "right":
				m.feedsList.CursorDown()
				if i, ok := m.feedsList.SelectedItem().(feedItem); ok {
					m.currentFeed = i.feed
					m.state = stateEntries
					m.loading = true
					m.entriesList.SetItems([]list.Item{})
					return m, m.loadEntries(i.feed)
				}
			case "esc", "backspace":
				m.state = stateFeeds
				return m, m.loadFeeds
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
			case "right":
				m.entriesList.CursorDown()
				if i, ok := m.entriesList.SelectedItem().(entryItem); ok {
					m.loading = true
					m.viewport.SetContent("Loading next...")
					return m, m.viewEntry(i.entry)
				}
			case "left":
				m.entriesList.CursorUp()
				if i, ok := m.entriesList.SelectedItem().(entryItem); ok {
					m.loading = true
					m.viewport.SetContent("Loading previous...")
					return m, m.viewEntry(i.entry)
				}
			case "esc", "backspace":
				m.state = stateEntries
				return m, tea.Batch(m.loadFeeds, m.loadEntries(m.currentFeed))
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
		if msg.index >= 0 && msg.index < len(msg.items) {
			m.feedsList.Select(msg.index)
		}
		m.loading = false

	case entriesMsg:
		items := make([]list.Item, len(msg.entries))
		for i, e := range msg.entries {
			items[i] = entryItem{entry: e, feedLastReadAt: msg.lastReadAt}
		}
		m.entriesList.SetItems(items)
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
	case stateHelp:
		content = m.helpView()
	}

	return DocStyle.Render(header + "\n" + content)
}

// Commands
type feedsMsg struct {
	items []list.Item
	index int
}
type entriesMsg struct {
	entries    []db.Entry
	lastReadAt time.Time
}
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
	return feedsMsg{items: items, index: -1}
}

func (m Model) loadFeedsWithIndex(index int) tea.Cmd {
	return func() tea.Msg {
		msg := m.loadFeeds()
		if fMsg, ok := msg.(feedsMsg); ok {
			fMsg.index = index
			return fMsg
		}
		return msg
	}
}

func (m Model) loadEntries(feed db.Feed) tea.Cmd {
	return func() tea.Msg {
		entries, err := db.GetEntries(feed.ID)
		if err != nil {
			return errMsg(err)
		}
		// We capture the LastReadAt BEFORE we update it in the DB
		lastReadAt := feed.LastReadAt
		db.MarkFeedAsRead(feed.ID)
		return entriesMsg{entries: entries, lastReadAt: lastReadAt}
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
	return m.loadEntries(m.currentFeed)()
}

func (m Model) viewEntry(e db.Entry) tea.Cmd {
	return func() tea.Msg {
		db.MarkAsRead(e.ID)
		if m.renderer == nil {
			return contentMsg(e.Content)
		}
		out, _ := m.renderer.Render(e.Content)
		if out == "" || out == "\n" {
			out, _ = m.renderer.Render(e.Description)
		}
		return contentMsg(out)
	}
}

func (m Model) helpView() string {
	return TitleStyle.Render("Keyboard Shortcuts") + "\n\n" +
		lipgloss.JoinHorizontal(lipgloss.Top,
			lipgloss.NewStyle().Width(30).Render(
				lipgloss.JoinVertical(lipgloss.Left,
					"General",
					"  ?       Show/Hide Help",
					"  q       Quit",
					"",
					"Navigation",
					"  ↑/↓     Move Cursor",
					"  ←/→     Switch Feed/Article",
					"  Enter   Select/Open",
					"  Esc     Go Back",
				),
			),
			lipgloss.NewStyle().Width(30).Render(
				lipgloss.JoinVertical(lipgloss.Left,
					"Feeds View",
					"  alt+↑/↓ Move Feed",
					"  alt+j/k Move Feed",
					"  a       Add New Feed",
					"  d       Delete Feed",
					"  r       Refresh All",
					"",
					"Articles View",
					"  r       Refresh Current Feed",
					"  b       Open in Browser",
				),
			),
			lipgloss.NewStyle().Width(30).Render(
				lipgloss.JoinVertical(lipgloss.Left,
					"Reading View",
					"  ←/→     Prev/Next Article",
					"  b       Open in Browser",
					"",
					"Symbols",
					"  Pink Text   Unread (New items)",
				),
			),
		) + "\n\n(press any key to return)"
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

