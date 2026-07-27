package shell

import (
	"image/color"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/pluggableharness/agent/internal/tui/paint"
	"github.com/pluggableharness/agent/internal/tui/region"
	"github.com/pluggableharness/agent/internal/tui/theme"
	"github.com/pluggableharness/agent/internal/tui/ui"
	renderv1 "github.com/pluggableharness/agent/pkg/render/proto/v1"
)

// overlay is the modal prompt currently capturing the keyboard.
type overlay struct {
	title    string
	itemID   string
	preview  *renderv1.RenderTree
	rawInput string
	// restore is the focus to return to once the overlay clears.
	restore Focus
}

// Model is the shell's Bubble Tea model: layout, focus, keymap, and the
// composition of every region into one frame.
//
// The model is driven entirely by messages and never performs I/O, which is
// what lets it be tested through tea.WithoutRenderer or by calling Update
// directly with no terminal at all.
type Model struct {
	th      theme.Theme
	painter *paint.Painter
	keys    KeyMap
	store   *region.Store
	emit    func(Action)

	width  int
	height int
	layout Layout

	focus    Focus
	cursor   int
	expanded map[string]bool
	input    *input
	scroll   int
	pinned   bool
	// maxScroll is the deepest scroll offset the last painted frame allowed.
	// It is what lets scrolling back down to the bottom re-attach to the live
	// tail instead of leaving the transcript one notch short of it forever.
	maxScroll   int
	sidebarOpen bool

	agents agentRing

	overlay *overlay
	notice  *NoticeMsg
	status  StatusMsg
	// usage is nil until the first UsageMsg arrives. Nil means "not known
	// yet", which must render as absent rather than as zero — a gauge reading
	// 0% before any turn has run is a confident lie.
	usage     *UsageMsg
	workspace WorkspaceMsg
	edits     EditStatsMsg

	// interruptArmed tracks the first ctrl+c of the two-press quit sequence.
	interruptArmed bool
	quitting       bool
}

// Option configures a Model.
type Option func(*Model)

// WithTheme sets the theme. The default is theme.Dark.
func WithTheme(t theme.Theme) Option {
	return func(m *Model) {
		m.th = t
		m.painter = paint.New(t)
	}
}

// WithAgents replaces the selectable agent roster. An empty roster keeps the
// defaults, since the shell always needs something to display as active.
func WithAgents(agents []Agent) Option {
	return func(m *Model) { m.agents = newAgentRing(agents) }
}

// WithKeyMap replaces the default bindings.
func WithKeyMap(k KeyMap) Option { return func(m *Model) { m.keys = k } }

// WithEmitter sets the sink for operator-originated actions. Without one the
// shell still runs and paints; it simply has nowhere to send, which is the
// right behavior for a rendering-only test.
func WithEmitter(f func(Action)) Option { return func(m *Model) { m.emit = f } }

// New returns a Model ready to receive messages.
func New(opts ...Option) *Model {
	m := &Model{
		th:       theme.Dark(),
		keys:     DefaultKeyMap(),
		store:    region.NewStore(),
		expanded: map[string]bool{},
		input:    newInput(),
		agents:   newAgentRing(nil),
		focus:    FocusInput,
		pinned:   true,
		width:    80,
		height:   24,
	}
	m.painter = paint.New(m.th)

	for _, o := range opts {
		o(m)
	}

	m.relayout()

	return m
}

// Init implements tea.Model. The event source runs outside the program, so
// there is no startup command.
func (m *Model) Init() tea.Cmd { return nil }

// Store exposes the content store so a bridge can inspect what is placed.
func (m *Model) Store() *region.Store { return m.store }

// Focus reports which region currently owns the keyboard.
func (m *Model) Focus() Focus { return m.focus }

// Layout reports the geometry of the most recent frame.
func (m *Model) Layout() Layout { return m.layout }

func (m *Model) relayout() {
	m.layout = Solve(m.width, m.height, m.input.Lines(), m.sidebarOpen)
}

func (m *Model) send(a Action) {
	if m.emit != nil {
		m.emit(a)
	}
}

// Update implements tea.Model.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.relayout()
	case tea.KeyPressMsg:
		return m, m.handleKey(msg.String())
	case tea.MouseWheelMsg:
		m.handleWheel(msg)
	case PlaceMsg:
		m.store.Place(msg.Content, msg.Producer, msg.Sequence)
		m.store.ClearProducerStreams()
		m.relayout()
	case DeltaMsg:
		m.store.Delta(msg.TargetID, msg.Text)
	case SettledMsg:
		m.store.ClearStream(msg.TargetID)
	case PermissionMsg:
		m.openOverlay(msg)
	case DismissOverlayMsg:
		m.closeOverlay()
		m.notice = &NoticeMsg{Text: msg.Reason, Level: NoticeWarn}
	case NoticeMsg:
		m.notice = &msg
	case StatusMsg:
		m.status = msg
	case UsageMsg:
		m.usage = &msg
	case WorkspaceMsg:
		m.workspace = msg
	case EditStatsMsg:
		m.edits = msg
	}

	return m, nil
}

// wheelStep is how many transcript lines one wheel notch moves.
const wheelStep = 3

// handleWheel scrolls the transcript.
//
// Claiming the wheel is what stops the terminal from scrolling its own
// scrollback out from under a full-screen application — without it the gesture
// appears to work while actually moving the window behind the app. It applies
// regardless of which region holds the keyboard, because pointing at something
// and turning the wheel is not a focus operation.
func (m *Model) handleWheel(msg tea.MouseWheelMsg) {
	switch msg.Button {
	case tea.MouseWheelUp:
		m.scrollBy(-wheelStep)
	case tea.MouseWheelDown:
		m.scrollBy(wheelStep)
	default:
		// Horizontal wheel events have nothing to act on.
	}
}

func (m *Model) openOverlay(msg PermissionMsg) {
	m.overlay = &overlay{
		title:    msg.Title,
		itemID:   msg.ItemID,
		preview:  msg.Preview,
		rawInput: msg.RawInput,
		restore:  m.focus,
	}
}

func (m *Model) closeOverlay() {
	if m.overlay == nil {
		return
	}

	m.focus = m.overlay.restore
	m.overlay = nil
}

// activeLayer reports which keymap layer currently has precedence.
func (m *Model) activeLayer() Layer {
	if m.overlay != nil {
		return LayerOverlay
	}

	return LayerRegion
}

// handleKey routes a keypress through the layer stack: overlay, then the
// focused region, then global. A layer that handles the key stops propagation.
func (m *Model) handleKey(key string) tea.Cmd {
	// Any keypress other than the second ctrl+c disarms the quit sequence, so
	// an interrupt followed by ordinary typing never quits unexpectedly.
	armed := m.interruptArmed
	m.interruptArmed = false

	// A notice is transient: it occupies the composer's title, so the next
	// keystroke clears it and the title returns to naming the active agent.
	// The durable record of anything important is the transcript, not this.
	m.notice = nil

	if m.overlay != nil {
		return m.overlayKey(key)
	}

	if m.regionKey(key) {
		return nil
	}

	return m.globalKey(key, armed)
}

func (m *Model) globalKey(key string, armed bool) tea.Cmd {
	switch {
	case m.keys.Interrupt.Matches(key):
		if armed {
			m.quitting = true

			return tea.Quit
		}

		m.interruptArmed = true
		m.send(Interrupt{})
		m.notice = &NoticeMsg{Text: "interrupted — press ctrl+c again to quit", Level: NoticeWarn}
	case m.keys.Quit.Matches(key) && m.input.Empty():
		m.quitting = true

		return tea.Quit
	case m.keys.NextFocus.Matches(key):
		m.moveFocus(false)
	case m.keys.PrevFocus.Matches(key):
		m.moveFocus(true)
	case m.keys.CycleAgent.Matches(key):
		m.cycleAgent(false)
	case m.keys.ToggleSidebar.Matches(key):
		m.sidebarOpen = !m.sidebarOpen
		m.relayout()
	}

	return nil
}

// Agent reports the currently selected agent.
func (m *Model) Agent() Agent { return m.agents.Current() }

// cycleAgent advances the agent selection and announces it.
//
// The selection is local state: no ClientEvent carries an agent profile today.
// AgentSelected is emitted so a bridge can decide what the change means once
// the protocol grows an answer.
func (m *Model) cycleAgent(back bool) {
	next := m.agents.cycle(back)
	m.send(AgentSelected{Name: next.Name})
	m.notice = &NoticeMsg{Text: next.Name + " — " + next.Description, Level: NoticeInfo}
}

func (m *Model) moveFocus(back bool) {
	ring := focusRing(m.layout, m.hasContent(renderv1.Region_REGION_SIDEBAR))
	m.focus = cycleFocus(m.focus, ring, back)
	m.cursor = 0
}

// regionKey dispatches to the focused region's bindings, reporting whether the
// key was consumed.
func (m *Model) regionKey(key string) bool {
	switch m.focus {
	case FocusInput:
		return m.inputKey(key)
	case FocusMain, FocusSidebar:
		return m.contentKey(key)
	default:
		return false
	}
}

func (m *Model) inputKey(key string) bool {
	switch {
	case m.keys.Newline.Matches(key):
		m.input.Insert("\n")
	case m.keys.Submit.Matches(key):
		if text, ok := m.input.Submit(); ok {
			m.send(SubmitPrompt{Text: text})
			m.pinned = true
		}
	case m.keys.HistoryPrev.Matches(key) && m.input.OnFirstLine():
		m.input.HistoryPrev()
	case m.keys.HistoryNext.Matches(key) && m.input.OnLastLine():
		m.input.HistoryNext()
	case key == "backspace":
		m.input.Backspace()
	case key == "left":
		m.input.Left()
	case key == "right":
		m.input.Right()
	case key == "home":
		m.input.Home()
	case key == "end":
		m.input.End()
	case key == "space":
		// Bubble Tea reports space by name rather than as its literal
		// character, so it never reaches the printable check below.
		m.input.Insert(" ")
	case isPrintable(key):
		m.input.Insert(key)
	default:
		return false
	}

	m.relayout()

	return true
}

func (m *Model) contentKey(key string) bool {
	targets := m.targets(m.focusedRegion())

	switch {
	case m.keys.Up.Matches(key):
		m.moveCursor(-1, len(targets))
	case m.keys.Down.Matches(key):
		m.moveCursor(1, len(targets))
	case m.keys.PageUp.Matches(key):
		m.scrollBy(-m.layout.MainInnerHeight())
	case m.keys.PageDown.Matches(key):
		m.scrollBy(m.layout.MainInnerHeight())
	case m.keys.Top.Matches(key):
		m.scroll, m.pinned = 0, false
	case m.keys.Bottom.Matches(key):
		m.pinned = true
	case m.keys.Activate.Matches(key):
		m.activate(targets)
	default:
		return false
	}

	return true
}

func (m *Model) moveCursor(delta, n int) {
	if n == 0 {
		m.scrollBy(delta)

		return
	}

	m.cursor = clamp(m.cursor+delta, 0, n-1)
}

func (m *Model) scrollBy(delta int) {
	m.scroll = clamp(m.scroll+delta, 0, m.maxScroll)
	// Reaching the bottom re-attaches to the live tail, which is what makes
	// scrolling down feel like catching up rather than like getting stuck one
	// line short of the newest content.
	m.pinned = m.scroll >= m.maxScroll
}

// activate fires the focused target: an action node dispatches unchanged to
// the kernel, a collapsible toggles locally and sends nothing.
func (m *Model) activate(targets []paint.Target) {
	if m.cursor < 0 || m.cursor >= len(targets) {
		return
	}

	t := targets[m.cursor]
	switch t.Kind {
	case paint.TargetCollapsible:
		m.expanded[t.Path] = !m.isExpanded(t.Path)
	case paint.TargetAction:
		m.send(Trigger{
			NodeID:   t.Action.GetId(),
			ToolName: t.Action.GetToolName(),
			Provider: t.Action.GetProvider(),
			Args:     t.Action.GetArgs(),
		})
	}
}

func (m *Model) isExpanded(path string) bool {
	if v, ok := m.expanded[path]; ok {
		return v
	}

	return false
}

func (m *Model) overlayKey(key string) tea.Cmd {
	switch {
	case m.keys.Allow.Matches(key):
		m.resolve(true, ScopeOnce)
	case m.keys.Deny.Matches(key):
		m.resolve(false, ScopeOnce)
	case m.keys.AllowSession.Matches(key):
		m.resolve(true, ScopeSession)
	case m.keys.Dismiss.Matches(key):
		// Dismiss never resolves a pending decision — it would be indefensible
		// to turn "go away" into an allow or a deny on the operator's behalf.
		m.notice = &NoticeMsg{Text: "decision still pending", Level: NoticeWarn}
	}

	return nil
}

func (m *Model) resolve(allow bool, scope DecisionScope) {
	if m.overlay == nil {
		return
	}

	m.send(Decision{ItemID: m.overlay.itemID, Allow: allow, Scope: scope})
	m.closeOverlay()
}

func (m *Model) focusedRegion() renderv1.Region {
	if m.focus == FocusSidebar {
		return renderv1.Region_REGION_SIDEBAR
	}

	return renderv1.Region_REGION_MAIN_CHAT
}

func (m *Model) hasContent(r renderv1.Region) bool {
	return len(m.store.Contents(r)) > 0
}

// targets enumerates the focusable elements of a region, giving each
// placement a distinct root path so paths stay unique across producers.
func (m *Model) targets(r renderv1.Region) []paint.Target {
	var out []paint.Target

	for i, pl := range m.store.Contents(r) {
		out = append(out, paint.TargetsAt(pl.Tree, m.expanded, strconv.Itoa(i))...)
	}

	return out
}

func isPrintable(key string) bool {
	if len([]rune(key)) != 1 {
		return false
	}

	r := []rune(key)[0]

	return r >= 0x20 && r != 0x7f
}

// focusedActionID returns the ActionNode ID under the cursor in a region, or
// empty when the cursor is elsewhere or on a non-action target.
func (m *Model) focusedActionID(r renderv1.Region, focused bool) string {
	if !focused {
		return ""
	}

	targets := m.targets(r)
	if m.cursor < 0 || m.cursor >= len(targets) || targets[m.cursor].Kind != paint.TargetAction {
		return ""
	}

	return targets[m.cursor].Action.GetId()
}

// focusedPath returns the node path under the cursor in a region.
func (m *Model) focusedPath(r renderv1.Region) string {
	targets := m.targets(r)
	if m.cursor < 0 || m.cursor >= len(targets) {
		return ""
	}

	return targets[m.cursor].Path
}

// paintRegion renders every placement in a region, joined vertically.
func (m *Model) paintRegion(r renderv1.Region, width int, focused bool) string {
	placements := m.store.Contents(r)
	if len(placements) == 0 {
		return ""
	}

	items := make([]sidebarItem, 0, len(placements))
	for i, pl := range placements {
		items = append(items, sidebarItem{index: i, tree: pl.Tree})
	}

	return m.paintItems(items, width, m.focusedActionID(r, focused))
}

// View implements tea.Model.
//
// The frame is composed as a stack of full-width bands — top bar, body,
// composer, status bar — each of which paints every cell it claims. Nothing is
// left uncovered, which together with View's own background and foreground
// colors is what makes the shell read as an application that owns the terminal
// rather than as text printed into someone else's window.
func (m *Model) View() tea.View {
	v := tea.NewView(m.frame())
	v.AltScreen = true
	v.BackgroundColor = m.th.C.Background
	v.ForegroundColor = m.th.C.Text
	v.WindowTitle = m.windowTitle()

	// Claim the mouse so the wheel scrolls this transcript rather than the
	// terminal's own scrollback behind the alt screen. The tradeoff is that
	// drag-selection needs the terminal's override (shift+drag in most).
	v.MouseMode = tea.MouseModeCellMotion

	// The real terminal cursor is placed in the composer rather than drawing a
	// caret glyph, so it blinks and behaves the way every other terminal
	// application's cursor does. It is hidden whenever the composer does not
	// own the keyboard.
	if m.focus == FocusInput && m.overlay == nil && !m.quitting {
		if x, y, ok := m.cursorScreenPos(); ok {
			c := tea.NewCursor(x, y)
			c.Color = m.th.C.Primary
			c.Blink = true
			v.Cursor = c
		}
	}

	return v
}

func (m *Model) windowTitle() string {
	if m.status.Session == "" {
		return "pluggableharness"
	}

	return "pluggableharness — " + m.status.Session
}

func (m *Model) frame() string {
	if m.quitting {
		return ""
	}

	l := m.layout
	rows := make([]string, 0, l.Height)

	if l.ShowTopBar {
		rows = append(rows, m.viewTopBar(l)...)
	}

	rows = append(rows, m.viewBody(l)...)
	rows = append(rows, m.viewComposer(l)...)

	if l.ShowStatus {
		rows = append(rows, m.statusLine(l))
	}

	if l.ShowHints {
		rows = append(rows, m.viewHints(l)...)
	}

	frame := strings.Join(rows, "\n")
	if m.overlay != nil {
		frame = m.viewOverlay(l, frame)
	}

	return frame
}

// app is the utility style for cells that belong to the application surface
// rather than to any panel — gutters, gaps, and the space around panels.
func (m *Model) app() ui.Style {
	return ui.New().Fg(m.th.C.TextSubtle)
}

// viewTopBar names what the session is working on.
//
// It is a bordered panel rather than a tinted line. A background fill is the
// obvious way to mark chrome and it does not carry: at these contrast levels a
// tinted row reads as a slightly-off content row, not as a frame. A box reads
// as a box. Its title carries the product name, which is both branding and the
// cheapest possible way to make the border feel deliberate.
//
// The agent is deliberately absent: it belongs beside the composer with the
// other settings that change when it does. What sits here is the answer to
// "where am I" — stable for the whole session, and the first thing an operator
// checks when returning to a window.
func (m *Model) viewTopBar(l Layout) []string {
	w := m.workspace

	left := []ui.Segment{
		{Value: ui.New().Fg(m.th.C.Text).Bold().Render(m.workspaceDir(l))},
		{Value: w.Repository, Tone: m.th.C.TextMuted},
	}

	// Session and run state read as one unit, so they share a segment rather
	// than being separated by a divider that implies they are different fields.
	session := ui.New().Fg(m.th.C.TextSubtle).Render(m.status.Session)
	if m.status.Status != "" {
		if session != "" {
			session += "  "
		}

		session += ui.Badge(m.th, m.status.Status, m.th.C.Success)
	}

	return m.chromePanel(l, "pluggableharness", m.th.C.Primary, ui.StatusLine{
		Segments: left,
		Right:    []ui.Segment{{Value: session}},
		Width:    l.ChromeInnerWidth(),
		Flush:    true,
	}.Render(m.th))
}

// chromePanel wraps a single line of chrome in the same bordered box the rest
// of the interface uses, so the header and footer belong to the same visual
// language as the panels between them.
func (m *Model) chromePanel(l Layout, title string, accent color.Color, body string) []string {
	panel := ui.Panel{
		Title:  title,
		Body:   body,
		Width:  l.Width - 2*theme.Gutter,
		Height: chromePanelHeight,
		Accent: accent,
	}.Render(m.th)

	gutter := m.app().Render(strings.Repeat(" ", theme.Gutter))

	out := make([]string, 0, chromePanelHeight)
	for r := range strings.SplitSeq(panel, "\n") {
		out = append(out, gutter+r+gutter)
	}

	return out
}

// workspaceDir clips the directory from the left, so a long path keeps the tail
// that identifies it rather than the prefix every path shares.
func (m *Model) workspaceDir(l Layout) string {
	return ui.ClipLeft(m.workspace.Directory, max(l.Width/3, 12))
}

// viewBody composes the main panel beside the sidebar column, returning one
// string per screen row so the caller can stack bands without measuring.
func (m *Model) viewBody(l Layout) []string {
	main := ui.Panel{
		Title:   "conversation",
		Body:    m.mainBody(l),
		Width:   l.MainWidth,
		Height:  l.BodyHeight,
		Focused: m.focus == FocusMain,
		Accent:  m.panelAccent(m.focus == FocusMain),
	}.Render(m.th)

	mainRows := strings.Split(main, "\n")
	gutter := m.app().Render(strings.Repeat(" ", theme.Gutter))

	if !l.ShowSidebar {
		out := make([]string, 0, len(mainRows))
		for _, r := range mainRows {
			out = append(out, gutter+r+gutter)
		}

		return out
	}

	sideRows := m.sidebarColumn(l)
	gap := m.app().Render(strings.Repeat(" ", theme.Space1))

	out := make([]string, 0, l.BodyHeight)

	for i := range l.BodyHeight {
		mainRow, sideRow := "", ""
		if i < len(mainRows) {
			mainRow = mainRows[i]
		}

		if i < len(sideRows) {
			sideRow = sideRows[i]
		}

		out = append(out, gutter+mainRow+gap+ui.Fit(sideRow, l.SidebarWidth)+gutter)
	}

	return out
}

// agentColor is the active agent's tone resolved against the current theme.
func (m *Model) agentColor() color.Color { return m.th.Tone(m.agents.Current().Tone) }

func (m *Model) panelAccent(focused bool) color.Color {
	if focused {
		return m.th.C.Primary
	}

	return m.th.C.TextSubtle
}

// mainBody assembles the transcript: placed content, folded sidebar content
// when the terminal is too narrow for a sidebar, and any live streaming text.
func (m *Model) mainBody(l Layout) string {
	width := l.MainInnerWidth()
	parts := []string{m.paintRegion(renderv1.Region_REGION_MAIN_CHAT, width, m.focus == FocusMain)}

	if l.FoldSidebar() {
		parts = append(parts, m.paintRegion(renderv1.Region_REGION_SIDEBAR, width, false))
	}

	for _, s := range m.store.Streams() {
		parts = append(parts, m.th.Default.Width(width).Render(s.Text))
	}

	return m.window(strings.Join(nonEmpty(parts), "\n"), l.MainInnerHeight())
}

// sidebarColumn renders one panel per contributing producer, stacked.
//
// Giving each producer its own titled panel — rather than concatenating every
// widget into one undifferentiated column — is what makes it obvious which
// plugin contributed what, and it is the visual affordance widget authors
// design against.
func (m *Model) sidebarColumn(l Layout) []string {
	const minPanelHeight = 3

	groups := m.sidebarGroups()
	focused := m.focus == FocusSidebar
	activeRoot := rootOf(m.focusedPath(renderv1.Region_REGION_SIDEBAR))
	action := m.focusedActionID(renderv1.Region_REGION_SIDEBAR, focused)

	rows := make([]string, 0, l.BodyHeight)

	for _, p := range m.sessionPanels(l) {
		if l.BodyHeight-len(rows) < minPanelHeight {
			break
		}

		p.Width = l.SidebarWidth
		p.Height = min(lipgloss.Height(p.Body)+panelChrome, l.BodyHeight-len(rows))
		p.Accent = m.th.C.TextSubtle

		rows = append(rows, strings.Split(p.Render(m.th), "\n")...)
	}

	for _, g := range groups {
		remaining := l.BodyHeight - len(rows)
		if remaining < minPanelHeight {
			break
		}

		body := m.paintItems(g.items, l.SidebarInnerWidth(), action)
		hot := focused && g.holds(activeRoot)

		panel := ui.Panel{
			Title:   g.title,
			Body:    body,
			Width:   l.SidebarWidth,
			Height:  min(lipgloss.Height(body)+panelChrome, remaining),
			Focused: hot,
			Accent:  m.panelAccent(hot),
		}.Render(m.th)

		rows = append(rows, strings.Split(panel, "\n")...)
	}

	// Pad the column so it covers the full body height; an uncovered cell is
	// where the terminal's own background shows through.
	blank := m.app().Render(strings.Repeat(" ", l.SidebarWidth))
	for len(rows) < l.BodyHeight {
		rows = append(rows, blank)
	}

	return rows[:l.BodyHeight]
}

// sidebarItem is one placement plus its index within the region's ordered
// contents. The index is the node path root, so paths stay aligned with what
// targets() enumerates even though panels group placements by producer.
type sidebarItem struct {
	index int
	tree  *renderv1.RenderTree
}

type sidebarGroup struct {
	title string
	items []sidebarItem
}

// holds reports whether this group owns the given path root.
func (g sidebarGroup) holds(root string) bool {
	for _, it := range g.items {
		if strconv.Itoa(it.index) == root {
			return true
		}
	}

	return false
}

// sidebarGroups buckets sidebar placements by producer, preserving the store's
// priority ordering and each producer's first appearance within it.
func (m *Model) sidebarGroups() []sidebarGroup {
	var groups []sidebarGroup

	at := map[region.Producer]int{}

	for i, pl := range m.store.Contents(renderv1.Region_REGION_SIDEBAR) {
		g, ok := at[pl.Producer]
		if !ok {
			groups = append(groups, sidebarGroup{title: pl.Producer.Name})
			g = len(groups) - 1
			at[pl.Producer] = g
		}

		groups[g].items = append(groups[g].items, sidebarItem{index: i, tree: pl.Tree})
	}

	return groups
}

// paintItems renders a group's placements, each rooted at its own path.
func (m *Model) paintItems(items []sidebarItem, width int, focusedAction string) string {
	parts := make([]string, 0, len(items))

	for _, it := range items {
		parts = append(parts, m.painter.TreeAt(it.tree, paint.Opts{
			Width:         width,
			FocusedAction: focusedAction,
			Expanded:      m.expanded,
		}, strconv.Itoa(it.index)))
	}

	return strings.Join(parts, "\n")
}

// rootOf returns the leading path segment, which identifies the placement a
// target belongs to.
func rootOf(path string) string {
	if i := strings.IndexByte(path, '.'); i >= 0 {
		return path[:i]
	}

	return path
}

// window clips content to a visible height, pinning to the live tail unless the
// operator has scrolled away from it.
//
// Content shorter than the viewport is pushed to the *bottom* rather than left
// at the top. A transcript grows upward from the composer the way every chat
// interface does: the newest message belongs next to where the operator is
// typing, and the empty space belongs above it, out of the way. Top-anchoring
// instead strands the last message a screen away from the input.
func (m *Model) window(content string, height int) string {
	lines := strings.Split(content, "\n")

	if pad := height - len(lines); pad > 0 {
		lines = append(make([]string, pad), lines...)
	}

	m.maxScroll = max(len(lines)-height, 0)

	// While pinned, the offset tracks the tail rather than sitting at zero, so
	// the first scroll away from the live edge starts from where the operator
	// is actually looking instead of jumping to the top of the transcript.
	if m.pinned {
		m.scroll = m.maxScroll
	}

	top := clamp(m.scroll, 0, m.maxScroll)
	end := min(top+height, len(lines))

	return strings.Join(lines[top:end], "\n")
}

func (m *Model) viewComposer(l Layout) []string {
	prompt := ui.New().Fg(m.agentColor()).Bold().Render("› ")

	placeholder := ""
	if m.focus == FocusInput {
		placeholder = ui.New().Fg(m.th.C.TextSubtle).
			Render("ask anything, or / for commands")
	}

	body := m.input.render(placeholder)
	lines := strings.Split(body, "\n")

	for i := range lines {
		if i == 0 {
			lines[i] = prompt + lines[i]

			continue
		}

		lines[i] = "  " + lines[i]
	}

	// The agent takes the near corner and the model takes the far one.
	//
	// They answer different questions — the agent is *who you are talking to*
	// and changes on a keystroke, the model is *what is behind it* and changes
	// rarely — but run together in one title they became a four-part string in
	// which neither was findable, and the agent's color bled onto settings it
	// does not own. Split across the diagonal, the identity sits where the eye
	// already goes for a panel's name and the configuration sits out of the way
	// until looked for.
	title := m.agents.Current().Name
	if m.notice != nil {
		title = m.notice.Text
	}

	panel := ui.Panel{
		Title:   title,
		Caption: m.modelCaption(),
		Body:    strings.Join(lines, "\n"),
		Width:   l.Width - 2*theme.Gutter,
		Height:  l.ComposerHeight,
		Focused: m.focus == FocusInput,
		Accent:  m.noticeAccent(),
	}.Render(m.th)

	gutter := m.app().Render(strings.Repeat(" ", theme.Gutter))
	out := make([]string, 0, l.ComposerHeight)

	for _, r := range strings.Split(panel, "\n") {
		out = append(out, gutter+r+gutter)
	}

	return out
}

// modelCaption is the model configuration that rides in the composer's
// bottom-right corner: which model, and how it is set to reason.
//
// Thinking and effort are joined with a slash rather than given separators of
// their own. They are one setting read two ways — "extended thinking at high
// effort" — and promoting each to a peer of the model name made three equal
// fields out of a name and its two modifiers.
//
// Absent a model there is no caption at all: a lone "extended/high" names a
// setting without saying what it applies to.
func (m *Model) modelCaption() string {
	if m.status.Model == "" {
		return ""
	}

	reasoning := strings.Join(nonEmpty([]string{m.status.Thinking, m.status.Effort}), "/")

	return strings.Join(nonEmpty([]string{m.status.Model, reasoning}), "  ·  ")
}

// noticeAccent colors the composer's title by the severity of the most recent
// notice, which is how errors and rejected client events stay visible without
// stealing a row from the transcript.
func (m *Model) noticeAccent() color.Color {
	if m.notice == nil {
		// With nothing to report, the composer wears the active agent's color,
		// which is what keeps the current mode visible without spending a row
		// on saying so.
		return m.agentColor()
	}

	switch m.notice.Level {
	case NoticeError:
		return m.th.C.Danger
	case NoticeWarn:
		return m.th.C.Warning
	case NoticeInfo:
		return m.th.C.Info
	default:
		return m.th.C.TextMuted
	}
}

func (m *Model) viewHints(l Layout) []string {
	inner := l.ChromeInnerWidth()

	// The right-hand label names whatever currently owns the keyboard, which is
	// the overlay while one is up rather than the region underneath it.
	owner := m.focus.String()
	if m.overlay != nil {
		owner = "permission"
	}

	// Context pressure outranks the focus label. Which region holds the
	// keyboard is recoverable by looking at the borders; running out of context
	// is not visible anywhere else on this line.
	tone := m.th.C.Primary
	if warning := m.contextWarning(); warning != "" {
		owner, tone = warning, m.th.C.Danger
	}

	// The hints are long enough to crowd out the right-hand label, and a line
	// sheds its right group first — so without bounding them the warning would
	// be the thing that disappears. Keys are a reminder and always recoverable;
	// running out of context is news.
	room := inner - lipgloss.Width(owner) - lipgloss.Width(ui.SegmentSeparator)

	hints := m.keys.Hints(m.activeLayer(), m.focus, l.SidebarAvailable, max(room, 0))
	if contributed := m.paintRegion(renderv1.Region_REGION_HOTKEY_HINTS, room, false); contributed != "" {
		hints = ui.Clip(contributed, max(room, 0))
	}

	return m.chromePanel(l, "keys", m.th.C.TextSubtle, ui.StatusLine{
		Segments: []ui.Segment{{Value: hints, Tone: m.th.C.TextSubtle}},
		Right:    []ui.Segment{{Value: owner, Tone: tone}},
		Width:    inner,
		Flush:    true,
	}.Render(m.th))
}

// cursorScreenPos maps the composer's buffer cursor onto absolute screen
// coordinates.
func (m *Model) cursorScreenPos() (x, y int, ok bool) {
	l := m.layout

	line, col := m.input.CursorPos()
	if line >= l.InputHeight {
		return 0, 0, false
	}

	// Gutter, panel border, panel padding, then the two-cell prompt on line 0.
	x = theme.Gutter + 1 + panelPadding + 2 + col
	y = l.ComposerTop() + 1 + line

	if x >= l.Width || y >= l.Height {
		return 0, 0, false
	}

	return x, y, true
}

// viewOverlay composites the modal pane on top of the composed frame.
//
// The frame underneath is preserved rather than blanked: an operator deciding
// whether to allow a tool call needs to see the transcript that led to it, and
// a full-screen takeover would hide exactly the context the decision depends
// on. The pane is drawn on the element surface with an active border, which is
// what makes it read as elevated above the panels behind it.
func (m *Model) viewOverlay(l Layout, frame string) string {
	// The preferred width is two thirds of the screen, but the terminal always
	// wins: on a very small terminal the desired minimum exceeds the available
	// width, and a pane wider than the screen wraps and corrupts every row
	// beneath it.
	width := clamp(l.Width*2/3, 24, max(l.Width-2*theme.Space2, 1))
	inner := max(width-panelChrome-2*panelPadding, 1)

	parts := []string{ui.New().Fg(m.th.C.Text).Bold().Render(m.overlay.title)}

	switch {
	case m.overlay.preview != nil:
		parts = append(parts, "", m.painter.Tree(m.overlay.preview, paint.Opts{Width: inner}))
	case m.overlay.rawInput != "":
		// The plan/apply gate requires falling back to the raw input when a
		// provider supplied no preview, rather than showing nothing.
		parts = append(parts, "", m.th.Code.Width(inner).Render(m.overlay.rawInput))
	}

	parts = append(parts, "",
		ui.New().Fg(m.th.C.TextSubtle).
			Render(m.keys.Hints(LayerOverlay, m.focus, false, inner)))

	body := strings.Join(parts, "\n")
	height := min(lipgloss.Height(body)+panelChrome, max(l.Height-2, 3))

	pane := ui.Panel{
		Title:   "permission",
		Body:    body,
		Width:   width,
		Height:  height,
		Focused: true,
		Accent:  m.th.C.Warning,
	}.Render(m.th)

	x := max((l.Width-width)/2, 0)
	y := max((l.Height-height)/2, 0)

	return ui.Overlay(frame, pane, x, y)
}

func nonEmpty(in []string) []string {
	out := make([]string, 0, len(in))

	for _, s := range in {
		if s != "" {
			out = append(out, s)
		}
	}

	return out
}

// contextDangerAt is the fraction of the effective ceiling past which the
// status line says so in words.
// The hue itself is continuous across the ramp; this threshold governs only
// the text warning, which needs a discrete moment to appear at.
const contextDangerAt = 0.85

// contextFill reports context pressure as a fraction, and whether it is known.
func (m *Model) contextFill() (float64, bool) {
	// A non-positive ceiling means the kernel has not resolved a budget for
	// this session yet; dividing by it would invent a number.
	if m.usage == nil || m.usage.EffectiveCeiling <= 0 {
		return 0, false
	}

	return float64(m.usage.UsedTokens) / float64(m.usage.EffectiveCeiling), true
}

// contextTone resolves the pressure hue from the theme's gauge ramp, which runs
// green through amber to red by default and is configurable as a list of tone
// names rather than colors.
func (m *Model) contextTone(fill float64) color.Color {
	return m.th.GaugeRamp.At(m.th, fill)
}

// contextWarning returns the status-line warning for high context pressure, or
// empty when there is nothing to say.
//
// It deliberately names no command. Compaction in this system is automatic — a
// context provider declaring compactor: true receives the conversation history
// and returns a rewritten one on its own initiative — so there is no operator
// action like "/compact" to point at, and inventing one would be worse than
// saying nothing. The honest message is that room is running out.
func (m *Model) contextWarning() string {
	fill, ok := m.contextFill()
	if !ok || fill < contextDangerAt {
		return ""
	}

	return "context nearly full"
}

// statusLine is the single row beneath the composer.
//
// It carries only what changes during a turn — context, cache, cost, elapsed.
// Everything static about a session (model, directory, repository) lives in the
// sidebar instead, because a field that never changes does not earn a place
// beside the input box. Volatile data goes where the operator is already
// looking; reference data goes to the periphery.
func (m *Model) statusLine(l Layout) string {
	// Context is the only left-hand segment, and everything else is pinned
	// right. That is not cosmetic: a line drops left segments right-to-left, so
	// anything sitting beside context would survive at its expense — and
	// context is the field worth keeping longest. Alone on the left, it can
	// never be dropped, and it still reads as the growing middle because the
	// right cluster is what it grows against.
	var left []ui.Segment
	if seg, ok := m.contextSegment(); ok {
		left = append(left, seg)
	}

	right := []ui.Segment{}
	if rate, ok := m.usage.cacheRate(); ok {
		right = append(right, ui.Segment{Label: "cache", Value: formatPercent(rate), Tone: m.th.C.Info})
	}

	if m.usage != nil {
		right = append(right, ui.Segment{
			Label: "cost",
			Value: formatUSD(m.usage.CumulativeCostUSD),
			Tone:  m.th.C.Warning,
		})
	}

	right = append(right, ui.Segment{
		Label: "elapsed",
		Value: formatDuration(m.status.Elapsed),
		Tone:  m.th.C.TextMuted,
	})

	inset := m.app().Render(strings.Repeat(" ", StatusInset))

	return inset + ui.StatusLine{
		Segments: left,
		Right:    right,
		Width:    l.StatusWidth(),
	}.Render(m.th) + inset
}

// contextSegment is the growing middle of the status line: the gradient meter,
// the absolute figures, and the percentage.
//
// It sits between the fixed groups so it absorbs every spare cell — the meter
// is the one thing on the line that benefits from more room. The absolute
// figures ride immediately after the bar rather than at the far edge, which is
// what keeps a long bar from ending in a number marooned across the screen.
func (m *Model) contextSegment() (ui.Segment, bool) {
	fill, ok := m.contextFill()
	if !ok {
		return ui.Segment{}, false
	}

	tone := m.contextTone(fill)
	figures := formatTokens(m.usage.UsedTokens) + " / " + formatTokens(m.usage.EffectiveCeiling)

	return ui.Segment{
		MinWidth: minContextSegment,
		Fill: func(width int) string {
			label := ui.New().Fg(m.th.C.TextSubtle).Render("context ")
			tail := ui.New().Fg(m.th.C.TextMuted).Render(" "+figures+"  ") +
				ui.New().Fg(tone).Bold().Render(formatPercent(fill))

			bar := width - lipgloss.Width(label) - lipgloss.Width(tail)
			if bar < minMeterBar {
				return label + ui.New().Fg(tone).Render(figures+"  "+formatPercent(fill))
			}

			return label + ui.GradientMeter(m.th, m.th.GaugeRamp, bar, fill) + tail
		},
	}, true
}

// sessionPanels are the shell's own sidebar panels: the session reference data
// that used to crowd the composer.
//
// They are built here rather than contributed by a plugin because the shell
// already has the data — but they render exactly like a widget's panel, which
// is deliberate. A widget author looking at the sidebar should see one visual
// language, not shell chrome sitting apart from plugin content.
func (m *Model) sessionPanels(l Layout) []ui.Panel {
	inner := l.SidebarInnerWidth()

	usage := ui.Fields(m.th, []ui.Field{
		{Label: "out", Value: m.usage.tokens(func(u *UsageMsg) int64 { return u.OutputTokens })},
		{Label: "in", Value: m.usage.tokens(func(u *UsageMsg) int64 { return u.InputTokens + u.CacheReadTokens })},
		{Label: "cached", Value: m.usage.tokens(func(u *UsageMsg) int64 { return u.CacheWriteTokens })},
		{Label: "lines", Value: m.edits.summary()},
	}, inner)

	panels := make([]ui.Panel, 0, 1)
	for _, p := range []ui.Panel{
		{Title: "usage", Body: usage},
	} {
		if p.Body != "" {
			panels = append(panels, p)
		}
	}

	return panels
}
