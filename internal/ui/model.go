package ui

import (
	"clirss/internal/db"
	"clirss/internal/rss"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/filepicker"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	htmltomarkdown "github.com/JohannesKaufmann/html-to-markdown/v2"
	"github.com/mattn/go-runewidth"
)


type state int

const (
	paneFeeds state = iota
	paneEntries
	paneContent
)

const (
	stateMain state = iota
	stateAddingFeed
	stateImportingOPML
	stateExportingOPML
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
func (i feedItem) Description() string { return "" }
func (i feedItem) FilterValue() string { return i.feed.Title }

type entryItem struct {
	entry          db.Entry
	feedLastReadAt time.Time
}

func (i entryItem) Title() string {
	title := i.entry.Title
	if i.entry.PublishedAt.After(i.feedLastReadAt) {
		title = UnreadItemStyle.Render(i.entry.Title)
	}
	// Wrap the title text in an OSC 8 hyperlink
	return "\x1b]8;;" + i.entry.Link + "\x1b\\" + title + "\x1b]8;;\x1b\\"
}
func (i entryItem) Description() string { return "" }
func (i entryItem) FilterValue() string { return i.entry.Title }

type Model struct {
	state         state
	activePane    state
	previousState state
	feedsList     list.Model
	entriesList   list.Model
	viewport      viewport.Model
	textInput     textinput.Model
	filePicker    filepicker.Model
	spinner       spinner.Model
	loading       bool
	width       int
	height      int
	currentFeed db.Feed
	renderer    *glamour.TermRenderer
	initialLoadDone bool
	syncPending     int
	statusMsg       string
	// Stored pane dimensions for consistent rendering
	paneHeight   int
	feedsWidth   int
	entriesWidth int
	contentWidth int
}

func NewModel() Model {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))

	ti := textinput.New()
	ti.Placeholder = "RSS Feed URL"
	ti.Focus()

	fp := filepicker.New()
	fp.AllowedTypes = []string{".opml", ".xml"}
	fp.CurrentDirectory, _ = os.UserHomeDir()

	m := Model{
		state:       stateMain,
		activePane:  paneFeeds,
		feedsList:   list.New([]list.Item{}, list.NewDefaultDelegate(), 0, 0),
		entriesList: list.New([]list.Item{}, list.NewDefaultDelegate(), 0, 0),
		viewport:    viewport.New(0, 0),
		textInput:   ti,
		filePicker:  fp,
		spinner:     s,
		loading:     true, // Set to true initially so the user sees the spinner immediately
	}
	d := list.NewDefaultDelegate()
	d.ShowDescription = false
	d.SetHeight(1)
	m.feedsList.SetDelegate(d)
	m.feedsList.SetShowTitle(false)
	m.feedsList.SetShowStatusBar(false)
	m.feedsList.SetShowPagination(true)
	m.feedsList.SetShowHelp(false)
	m.feedsList.AdditionalFullHelpKeys = func() []key.Binding {
		return []key.Binding{
			key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
		}
	}
	ed := list.NewDefaultDelegate()
	ed.ShowDescription = false
	ed.SetHeight(1)
	m.entriesList.SetDelegate(ed)
	m.entriesList.SetShowTitle(false)
	m.entriesList.SetShowStatusBar(false)
	m.entriesList.SetShowPagination(true)
	m.entriesList.SetShowHelp(false)
	m.entriesList.AdditionalFullHelpKeys = m.feedsList.AdditionalFullHelpKeys

	return m
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(
		m.loadFeeds,
		m.spinner.Tick,
		m.startBackgroundSync,
	)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

		// Calculate pane widths (3 panes)
		feedsWidth := int(float64(msg.Width) * 0.2)
		if feedsWidth < 20 {
			feedsWidth = 20
		}
		entriesWidth := int(float64(msg.Width) * 0.25)
		if entriesWidth < 25 {
			entriesWidth = 25
		}
		// Total width = feedsWidth + entriesWidth + contentWidth + borders/padding
		// DocStyle margin: 2 left, 2 right = 4
		// Pane borders: 3 panes * 2 = 6
		contentWidth := msg.Width - feedsWidth - entriesWidth - 10
		if contentWidth < 30 {
			contentWidth = 30
		}

		// Height calculation:
		// msg.Height
		// - 1 (Status bar)
		// - 2 (Pane borders top+bottom)
		// = msg.Height - 3 for pane inner content height
		paneHeight := msg.Height - 3

		// Store dimensions for consistent rendering
		m.paneHeight = paneHeight
		m.feedsWidth = feedsWidth
		m.entriesWidth = entriesWidth
		m.contentWidth = contentWidth

		m.feedsList.SetSize(feedsWidth, paneHeight-1)
		m.entriesList.SetSize(entriesWidth, paneHeight-1)
		m.viewport.Width = contentWidth
		// Viewport height needs to account for the custom header we render in the View function
		m.viewport.Height = paneHeight - 1
		m.textInput.Width = msg.Width - 10
		m.filePicker.Height = msg.Height - 5

		// Update renderer
		if m.renderer == nil || m.width != msg.Width {
			r, _ := glamour.NewTermRenderer(
				glamour.WithStylePath("dark"),
				glamour.WithWordWrap(contentWidth-4),
				glamour.WithEmoji(),
			)
			m.renderer = r
		}
		return m, nil

	case tea.KeyMsg:
		m.statusMsg = "" // Clear status message on any keypress
		isFiltering := (m.feedsList.FilterState() == list.Filtering) ||
			(m.entriesList.FilterState() == list.Filtering)

		if msg.String() == "?" && m.state != stateHelp && m.state != stateAddingFeed && !isFiltering {
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
			return m, nil

		case stateMain:
			switch msg.String() {
			case "q":
				return m, tea.Quit
			case "enter":
				if i, ok := m.entriesList.SelectedItem().(entryItem); ok {
					openBrowser(i.entry.Link)
				}
				return m, nil
			case "tab", "right":
				m.activePane = (m.activePane + 1) % 3
				return m, nil
			case "shift+tab", "left":
				m.activePane = (m.activePane - 1 + 3) % 3
				return m, nil
			case "a":
				m.state = stateAddingFeed
				m.textInput.Focus()
				return m, nil
			case "i":
				m.state = stateImportingOPML
				return m, m.filePicker.Init()
			case "e":
				return m, m.exportOPML
			case "r":
				return m, m.refreshAllFeeds()
			}

			// Delegate to active pane
			var cmd tea.Cmd
			switch m.activePane {
			case paneFeeds:
				if m.feedsList.FilterState() == list.Filtering {
					m.feedsList, cmd = m.feedsList.Update(msg)
					return m, cmd
				}
				switch msg.String() {
				case "up", "down", "j", "k":
					m.feedsList, cmd = m.feedsList.Update(msg)
					if i, ok := m.feedsList.SelectedItem().(feedItem); ok {
						m.currentFeed = i.feed
						return m, tea.Batch(cmd, m.loadEntries(i.feed))
					}
					return m, cmd
				case "alt+up", "alt+k":
					idx := m.feedsList.Index()
					if idx > 0 {
						itemA := m.feedsList.Items()[idx].(feedItem)
						itemB := m.feedsList.Items()[idx-1].(feedItem)
						posA, posB := itemA.feed.Position, itemB.feed.Position
						if posA == posB {
							posA, posB = idx, idx-1
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
							posA, posB = idx, idx+1
						}
						db.SwapFeedPositions(int(itemA.feed.ID), posA, int(itemB.feed.ID), posB)
						return m, m.loadFeedsWithIndex(idx + 1)
					}
				case "d":
					if i, ok := m.feedsList.SelectedItem().(feedItem); ok {
						return m, m.deleteFeed(i.feed.ID)
					}
				}
				m.feedsList, cmd = m.feedsList.Update(msg)
				return m, cmd

			case paneEntries:
				if m.entriesList.FilterState() == list.Filtering {
					m.entriesList, cmd = m.entriesList.Update(msg)
					return m, cmd
				}
				switch msg.String() {
				case "up", "down", "j", "k":
					m.entriesList, cmd = m.entriesList.Update(msg)
					if i, ok := m.entriesList.SelectedItem().(entryItem); ok {
						return m, tea.Batch(cmd, m.viewEntry(i.entry))
					}
					return m, cmd
				case "r":
					return m, m.refreshCurrentFeed()
				}
				m.entriesList, cmd = m.entriesList.Update(msg)
				return m, cmd

			case paneContent:
				m.viewport, cmd = m.viewport.Update(msg)
				return m, cmd
			}

		case stateAddingFeed:
			switch msg.String() {
			case "esc":
				m.state = stateMain
				m.textInput.Reset()
				return m, nil
			case "enter":
				url := m.textInput.Value()
				m.state = stateMain
				m.textInput.Reset()
				m.loading = true
				return m, m.addFeed(url)
			}
			m.textInput, cmd = m.textInput.Update(msg)
			return m, cmd

		case stateImportingOPML:
			if msg.String() == "esc" {
				m.state = stateMain
				return m, nil
			}
			m.filePicker, cmd = m.filePicker.Update(msg)
			if didSelect, path := m.filePicker.DidSelectFile(msg); didSelect {
				m.state = stateMain
				m.loading = true
				return m, m.importOPML(path)
			}
			return m, cmd
		}

	case tea.MouseMsg:
		// Adjust for DocStyle padding (1 top, 2 left)
		msg.X -= 2
		msg.Y -= 1

		// Handle Scrolling
		if msg.Action == tea.MouseActionPress {
			if msg.Button == tea.MouseButtonWheelUp || msg.Button == tea.MouseButtonWheelDown {
				// Route scroll to the pane the mouse is currently over
				if msg.X < m.feedsList.Width() {
					m.feedsList, cmd = m.feedsList.Update(msg)
					return m, cmd
				} else if msg.X < m.feedsList.Width()+m.entriesList.Width()+2 {
					m.entriesList, cmd = m.entriesList.Update(msg)
					return m, cmd
				} else {
					m.viewport, cmd = m.viewport.Update(msg)
					return m, cmd
				}
			}
		}

		// Handle Clicking
		if msg.Type == tea.MouseLeft && msg.Action == tea.MouseActionRelease {
			if msg.X < m.feedsList.Width() {
				m.activePane = paneFeeds
				m.feedsList, cmd = m.feedsList.Update(msg)
				if i, ok := m.feedsList.SelectedItem().(feedItem); ok {
					m.currentFeed = i.feed
					return m, tea.Batch(cmd, m.loadEntries(i.feed))
				}
				return m, cmd
			} else if msg.X < m.feedsList.Width()+m.entriesList.Width()+2 {
				m.activePane = paneEntries
				m.entriesList, cmd = m.entriesList.Update(msg)
				if i, ok := m.entriesList.SelectedItem().(entryItem); ok {
					return m, tea.Batch(cmd, m.viewEntry(i.entry))
				}
				return m, cmd
			} else {
				m.activePane = paneContent
				m.viewport, cmd = m.viewport.Update(msg)
				return m, cmd
			}
		}

	case backgroundSyncMsg:
		m.syncPending = len(msg.feeds)
		var cmds []tea.Cmd
		for _, f := range msg.feeds {
			cmds = append(cmds, m.syncFeed(f))
		}
		return m, tea.Batch(cmds...)

	case feedSyncedMsg:
		m.syncPending--
		// Reload feeds list to update unread counts (but won't cascade into entries/content)
		return m, m.loadFeeds

	case feedsMsg:
		m.feedsList.SetItems(msg.items)
		if msg.index >= 0 && msg.index < len(msg.items) {
			m.feedsList.Select(msg.index)
		}
		m.loading = false
		// Only auto-load entries for the first feed on the very first load.
		// Subsequent reloads (from background sync) just update the list silently.
		if !m.initialLoadDone && len(msg.items) > 0 {
			m.initialLoadDone = true
			if i, ok := m.feedsList.SelectedItem().(feedItem); ok {
				m.currentFeed = i.feed
				return m, m.loadEntries(i.feed)
			}
		}

	case entriesMsg:
		items := make([]list.Item, len(msg.entries))
		for i, e := range msg.entries {
			items[i] = entryItem{entry: e, feedLastReadAt: msg.lastReadAt}
		}
		m.entriesList.SetItems(items)
		m.loading = false
		// Load content for the first entry automatically
		if len(items) > 0 {
			if i, ok := m.entriesList.SelectedItem().(entryItem); ok {
				return m, m.viewEntry(i.entry)
			}
		}

	case contentMsg:
		m.viewport.SetContent(string(msg))
		m.loading = false

	case exportMsg:
		m.statusMsg = string(msg)
		m.loading = false

	case errMsg:
		// Display error in the content pane instead of crashing
		m.viewport.SetContent(ErrorStyle.Render(fmt.Sprintf("Error: %v", msg)))
		m.loading = false

	case spinner.TickMsg:
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	}

	// Route updates to sub-models
	switch m.state {
	case stateMain:
		m.feedsList, cmd = m.feedsList.Update(msg)
		cmds = append(cmds, cmd)
		m.entriesList, cmd = m.entriesList.Update(msg)
		cmds = append(cmds, cmd)
		m.viewport, cmd = m.viewport.Update(msg)
		cmds = append(cmds, cmd)
	case stateAddingFeed:
		m.textInput, cmd = m.textInput.Update(msg)
		cmds = append(cmds, cmd)
	case stateImportingOPML:
		m.filePicker, cmd = m.filePicker.Update(msg)
		cmds = append(cmds, cmd)
	}

	return m, tea.Batch(cmds...)
}

func (m Model) View() string {
	if m.state == stateHelp {
		return DocStyle.Render(m.helpView())
	}

	if m.state == stateAddingFeed {
		return DocStyle.Render(TitleStyle.Render("Add Feed") + "\n\n" +
			"Enter URL:\n\n" + m.textInput.View() + "\n\n(esc to cancel)")
	}

	if m.state == stateImportingOPML {
		return DocStyle.Render(TitleStyle.Render("Import OPML") + "\n\n" +
			m.filePicker.View() + "\n\n(esc to cancel)")
	}

	// Main 3-pane view
	feedsStyle := InactivePaneStyle.Copy()
	entriesStyle := InactivePaneStyle.Copy()
	contentStyle := InactivePaneStyle.Copy()

	switch m.activePane {
	case paneFeeds:
		feedsStyle = ActivePaneStyle.Copy()
	case paneEntries:
		entriesStyle = ActivePaneStyle.Copy()
	case paneContent:
		contentStyle = ActivePaneStyle.Copy()
	}

	// Use stored pane dimensions, with fallbacks for the very first frame
	h := m.paneHeight
	if h <= 0 {
		h = 20
	}
	fw := m.feedsWidth
	if fw <= 0 {
		fw = 20
	}
	ew := m.entriesWidth
	if ew <= 0 {
		ew = 25
	}
	cw := m.contentWidth
	if cw <= 0 {
		cw = 40
	}

	feedsTitle := m.feedsList.Styles.Title.Copy().MarginLeft(2).Render("Feeds")
	feedsView := feedsStyle.Width(fw).Height(h).Render(lipgloss.JoinVertical(lipgloss.Left, feedsTitle, m.feedsList.View()))

	var entriesTitle string
	if m.currentFeed.ID != 0 {
		osc8Start := "\x1b]8;;" + m.currentFeed.URL + "\x1b\\"
		osc8End := "\x1b]8;;\x1b\\"
		// Truncate the visible text to avoid overflow, then wrap in OSC 8
		title := runewidth.Truncate(m.currentFeed.Title, ew-6, "...")
		entriesTitle = m.entriesList.Styles.Title.Copy().MarginLeft(2).Render(osc8Start + title + osc8End)
	} else {
		entriesTitle = m.entriesList.Styles.Title.Copy().MarginLeft(2).Render("Articles")
	}
	entriesView := entriesStyle.Width(ew).Height(h).Render(lipgloss.JoinVertical(lipgloss.Left, entriesTitle, m.entriesList.View()))

	var contentTitle string
	if i, ok := m.entriesList.SelectedItem().(entryItem); ok {
		// Make the Article Title itself clickable
		osc8Start := "\x1b]8;;" + i.entry.Link + "\x1b\\"
		osc8End := "\x1b]8;;\x1b\\"
		title := runewidth.Truncate(i.entry.Title, cw-6, "...")
		contentTitle = osc8Start + title + osc8End
	}
	contentHeader := TitleStyle.Copy().MaxHeight(1).Render(contentTitle)
	contentView := contentStyle.Width(cw).Height(h).Render(lipgloss.JoinVertical(lipgloss.Left, contentHeader, m.viewport.View()))

	mainView := lipgloss.JoinHorizontal(lipgloss.Top, feedsView, entriesView, contentView)

	// Status Bar: pill on left, status in middle, help hint on right
	totalWidth := fw + ew + cw + 6
	pill := StatusPillStyle.Render("Lazy RSS")
	pillWidth := lipgloss.Width(pill)

	helpHint := StatusHelpStyle.Render("? help")
	helpWidth := lipgloss.Width(helpHint)

	midText := ""
	if m.loading {
		midText = m.spinner.View() + " Loading..."
	} else if m.syncPending > 0 {
		midText = m.spinner.View() + " Syncing feeds..."
	} else if m.statusMsg != "" {
		midText = m.statusMsg
	}
	midWidth := totalWidth - pillWidth - helpWidth
	if midWidth < 0 {
		midWidth = 0
	}
	mid := StatusTextStyle.Width(midWidth).Render(midText)

	statusBar := lipgloss.JoinHorizontal(lipgloss.Top, pill, mid, helpHint)

	return DocStyle.Render(lipgloss.JoinVertical(lipgloss.Left, mainView, statusBar))
}

// Commands
type feedsMsg struct {
	items []list.Item
	index int
}
type backgroundSyncMsg struct {
	feeds []db.Feed
}
type feedSyncedMsg struct {
	feedID int64
}
type entriesMsg struct {
	entries    []db.Entry
	lastReadAt time.Time
}
type contentMsg string
type exportMsg string

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

func (m Model) startBackgroundSync() tea.Msg {
	feeds, err := db.GetFeeds()
	if err != nil {
		return errMsg(err)
	}
	return backgroundSyncMsg{feeds: feeds}
}

func (m Model) syncFeed(f db.Feed) tea.Cmd {
	return func() tea.Msg {
		err := rss.SyncFeed(f.ID, f.URL)
		if err != nil {
			return errMsg(err)
		}
		return feedSyncedMsg{feedID: f.ID}
	}
}

func (m Model) refreshAllFeeds() tea.Cmd {
	return m.startBackgroundSync
}

func (m Model) refreshCurrentFeed() tea.Cmd {
	return func() tea.Msg {
		err := rss.SyncFeed(m.currentFeed.ID, m.currentFeed.URL)
		if err != nil {
			return errMsg(err)
		}
		return m.loadEntries(m.currentFeed)()
	}
}

func (m Model) importOPML(path string) tea.Cmd {
	return func() tea.Msg {
		f, err := os.Open(path)
		if err != nil {
			return errMsg(err)
		}
		defer f.Close()

		opml, err := rss.ParseOPML(f)
		if err != nil {
			return errMsg(err)
		}

		outlines := opml.Flatten()
		for _, out := range outlines {
			title := out.Title
			if title == "" {
				title = out.Text
			}
			_, _ = db.AddFeed(out.XMLURL, title, "")
		}

		return m.loadFeeds()
	}
}

func (m Model) exportOPML() tea.Msg {
	feeds, err := db.GetFeeds()
	if err != nil {
		return errMsg(err)
	}

	var outlines []rss.Outline
	for _, f := range feeds {
		outlines = append(outlines, rss.Outline{
			Text:   f.Title,
			Title:  f.Title,
			Type:   "rss",
			XMLURL: f.URL,
		})
	}

	data, err := rss.GenerateOPML(outlines)
	if err != nil {
		return errMsg(err)
	}

	home, _ := os.UserHomeDir()
	exportPath := filepath.Join(home, "Downloads", "feeds_export.opml")
	err = os.WriteFile(exportPath, data, 0644)
	if err != nil {
		return errMsg(err)
	}

	return exportMsg(fmt.Sprintf("Exported to %s", exportPath))
}

func (m Model) renderMarkdown(md string) string {
	if m.renderer == nil {
		return md
	}

	// Pre-process: convert linked images [![alt](img-url)](link-url)
	// into simple images ![alt](link-url) so the main regex can handle them.
	reLinkedImg := regexp.MustCompile(`\[!\[([^\]]*)\]\([^)]+\)\]\(([^)]+)\)`)
	md = reLinkedImg.ReplaceAllString(md, "![$1]($2)")

	// Tokenize links and images to avoid showing URLs.
	// The regex captures: 1: optional '\', 2: optional '!', 3: link text/alt, 4: url
	re := regexp.MustCompile(`(\\)?(!)?\[([^\]]*)\]\(\s*([^\s\)]+)(?:\s+["'][^"']*["'])?\s*\)`)
	type linkInfo struct {
		text    string
		url     string
		isImage bool
	}
	links := make(map[string]linkInfo)
	tokenCount := 0

	// Pre-process markdown to replace links with unique tokens
	processedMD := re.ReplaceAllStringFunc(md, func(match string) string {
		submatch := re.FindStringSubmatch(match)
		if len(submatch) < 5 {
			return match
		}
		// Use a token that Glamour won't interpret as Markdown
		token := fmt.Sprintf("GLAMOURTOKEN%dURL", tokenCount)
		text := submatch[3]
		isImage := submatch[2] == "!"
		if text == "" && isImage {
			text = "Image"
		} else if text == "" {
			text = "Link"
		}

		links[token] = linkInfo{
			isImage: isImage,
			text:    text,
			url:     submatch[4],
		}
		tokenCount++
		return token
	})

	// Render with glamour
	rendered, err := m.renderer.Render(processedMD)
	if err != nil {
		return md
	}

	// Post-process to insert OSC 8 links
	for token, info := range links {
		displayText := info.text
		if info.isImage {
			displayText = "[img] " + displayText
		}

		// Style the link text (blue and underlined)
		styledText := lipgloss.NewStyle().
			Foreground(lipgloss.Color("12")).
			Underline(true).
			Render(displayText)

		// Create OSC 8 hyperlink
		osc8 := fmt.Sprintf("\x1b]8;;%s\x1b\\%s\x1b]8;;\x1b\\", info.url, styledText)
		rendered = strings.ReplaceAll(rendered, token, osc8)
	}

	return strings.TrimSpace(rendered)
}

func (m Model) viewEntry(e db.Entry) tea.Cmd {
	return func() tea.Msg {
		db.MarkAsRead(e.ID)

		// Build metadata (published date + link), each on its own line, indented
		metaStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("241")).PaddingLeft(2)
		var metaLines []string
		if !e.PublishedAt.IsZero() {
			metaLines = append(metaLines, metaStyle.Render(e.PublishedAt.Format("Mon, 02 Jan 2006 15:04")))
		}
		if e.Link != "" {
			linkOsc := fmt.Sprintf("\x1b]8;;%s\x1b\\%s\x1b]8;;\x1b\\",
				e.Link,
				lipgloss.NewStyle().Foreground(lipgloss.Color("12")).Underline(true).Render(e.Link))
			metaLines = append(metaLines, metaStyle.Render(linkOsc))
		}

		var out string
		if len(metaLines) > 0 {
			out += "\n" + strings.Join(metaLines, "\n") + "\n\n"
		}

		// Convert HTML to Markdown for both description and content
		descMD, _ := htmltomarkdown.ConvertString(e.Description)
		contentMD, _ := htmltomarkdown.ConvertString(e.Content)

		if descMD != "" {
			renderedDesc := m.renderMarkdown(descMD)
			if renderedDesc != "" && renderedDesc != "\n" {
				out += DescriptionReadingStyle.Render(renderedDesc)
			}
		}

		if contentMD != "" && contentMD != descMD {
			renderedContent := m.renderMarkdown(contentMD)
			if renderedContent != "" && renderedContent != "\n" {
				if out != "" {
					out += "\n\n"
				}
				out += renderedContent
			}
		}

		if out == "" {
			out = "No content available."
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
					"  Tab/→   Next Pane",
					"  S-Tab/← Prev Pane",
					"  Enter   Open in Browser",
					"",
					"Navigation",
					"  ↑/↓     Move Cursor",
					"  Esc     Go Back",
				),
			),
			lipgloss.NewStyle().Width(30).Render(
				lipgloss.JoinVertical(lipgloss.Left,
					"Feeds View",
					"  alt+↑/↓ Move Feed",
					"  alt+j/k Move Feed",
					"  a       Add New Feed",
					"  i       Import OPML",
					"  e       Export OPML",
					"  d       Delete Feed",
					"  r       Refresh All",
					"",
					"Articles View",
					"  r       Refresh Current Feed",
				),
			),
			lipgloss.NewStyle().Width(30).Render(
				lipgloss.JoinVertical(lipgloss.Left,
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

