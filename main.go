package main

import (
	"bufio"
	"embed"
	"fmt"
	"math/rand"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

//go:embed data/mots.txt data/secrets.txt
var dataFS embed.FS

const maxTries = 6

type level struct{ letters, hints int }

var levels = []level{{5, 2}, {6, 1}, {7, 0}, {8, 0}, {9, 0}}

var dict = map[int][]string{}
var secrets = map[int][]string{}

var (
	titleStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.AdaptiveColor{Light: "#D6488C", Dark: "#FF9EC4"})
	goodStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.AdaptiveColor{Light: "#B3243C", Dark: "#FF7B8A"})
	presStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.AdaptiveColor{Light: "#1F5FA8", Dark: "#7BB8FF"})
	missStyle  = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#8A8A8A", Dark: "#6A6A6A"})
	givenStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.AdaptiveColor{Light: "#444444", Dark: "#DDDDDD"})
	cursorSty  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.AdaptiveColor{Light: "#D6488C", Dark: "#FF9EC4"})
	msgStyle   = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#555555", Dark: "#BBBBBB"})
	helpStyle  = lipgloss.NewStyle().Faint(true)
)

type model struct {
	menu    bool
	level   int
	secret  string
	len     int
	grid    [][]rune // tries joués
	states  [][]byte // 'G','P','-'
	current []rune
	locked  []bool
	cursor  int
	over    bool
	won     bool
	message string
	def     string
	width   int
	height  int
}

func loadData() {
	for _, f := range []struct {
		name string
		m    map[int][]string
	}{{"data/mots.txt", dict}, {"data/secrets.txt", secrets}} {
		file, err := dataFS.Open(f.name)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(file)
		for sc.Scan() {
			w := strings.TrimSpace(sc.Text())
			if len(w) >= 5 && len(w) <= 9 {
				f.m[len(w)] = append(f.m[len(w)], w)
			}
		}
		file.Close()
	}
}

func score(guess, secret string) (string, []bool) {
	fb := make([]byte, len(secret))
	locked := make([]bool, len(secret))
	rem := []rune(secret)
	for i, g := range guess {
		if g == rune(secret[i]) {
			fb[i] = 'G'
			locked[i] = true
			for j, r := range rem {
				if r == g && int(j) >= 0 {
					rem = append(rem[:j], rem[j+1:]...)
					break
				}
			}
		}
	}
	for i, g := range guess {
		if fb[i] == 'G' {
			continue
		}
		for j, r := range rem {
			if r == g {
				fb[i] = 'P'
				rem = append(rem[:j], rem[j+1:]...)
				break
			}
		}
	}
	return string(fb), locked
}

func firstFree(m *model, dir int) int {
	for i := 0; i < m.len; i++ {
		if !m.locked[i] {
			return i
		}
	}
	return m.len
}

func nextFreeAfter(m *model, from int) int {
	for i := from + 1; i < m.len; i++ {
		if !m.locked[i] {
			return i
		}
	}
	return m.len
}

func prevFreeBefore(m *model, from int) int {
	if from >= m.len {
		from = m.len - 1 // from peut être la sentinelle m.len (saisie complète)
	}
	for i := from; i >= 0; i-- {
		if !m.locked[i] {
			return i
		}
	}
	return m.len // plus de case libre : pas de déplacement
}

func (m *model) start(level int) {
	m.level = level
	m.len = levels[level].letters
	pool := secrets[m.len]
	if len(pool) == 0 {
		pool = dict[m.len]
	}
	if len(pool) == 0 {
		m.message = "Dictionnaire introuvable (＞﹏＜"
		m.menu = true
		return
	}
	m.secret = pool[rand.Intn(len(pool))]
	m.grid = nil
	m.states = nil
	m.current = make([]rune, m.len)
	m.current[0] = rune(m.secret[0])
	m.locked = make([]bool, m.len)
	m.locked[0] = true
	m.cursor = firstFree(m, 1)
	m.over, m.won, m.def = false, false, ""
	m.message = "Mot de " + fmt.Sprint(m.len) + " lettres · entre 1 et " + fmt.Sprint(maxTries) + " ♪"
	m.menu = false
}

func (m *model) Init() tea.Cmd { return nil }

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case defMsg:
		m.def = string(msg)
	case tea.KeyMsg:
		k := msg.String()
		if m.menu {
			switch k {
			case "1", "2", "3", "4", "5":
				m.start(int(k[0] - '1'))
			case "down", "j", "right", "l":
				m.level = (m.level + 1) % len(levels)
			case "up", "k", "left", "h":
				m.level = (m.level + len(levels) - 1) % len(levels)
			case "enter":
				m.start(m.level)
			case "q", "ctrl+c", "esc":
				return m, tea.Quit
			}
			return m, nil
		}
		switch k {
		case "q":
			// lettre en pleine partie ; quitter est réservé au menu
			if m.over {
				return m, tea.Quit
			}
			m.typeRune('q')
		case "ctrl+c":
			return m, tea.Quit
		case "esc":
			m.menu = true
		case "n":
			if m.over {
				m.start(m.level)
			} else {
				m.typeRune('n')
			}
		case "left":
			m.cursor = prevFreeBefore(m, m.cursor)
		case "right":
			m.cursor = nextFreeAfter(m, m.cursor)
		case "backspace":
			m.backspace()
		case "enter", " ", "space":
			m.submit()
		default:
			if len(k) == 1 && k[0] >= 'a' && k[0] <= 'z' {
				m.typeRune(rune(k[0]))
			}
		}
	}
	if len(defCmds) > 0 {
		cmds := defCmds
		defCmds = nil
		return m, tea.Batch(cmds...)
	}
	return m, nil
}

func (m *model) typeRune(r rune) {
	if m.over || m.cursor >= m.len || m.locked[m.cursor] {
		return
	}
	m.current[m.cursor] = r
	m.cursor = nextFreeAfter(m, m.cursor)
	m.message = "Entrée pour valider ♪"
}

func (m *model) backspace() {
	if m.cursor < m.len && !m.locked[m.cursor] && m.current[m.cursor] != 0 {
		m.current[m.cursor] = 0
		return
	}
	for c := m.cursor - 1; c >= 0; c-- {
		if !m.locked[c] && m.current[c] != 0 {
			m.current[c] = 0
			m.cursor = c
			return
		}
	}
}

type defMsg string

func fetchDef(word string) tea.Cmd {
	return func() tea.Msg {
		client := &http.Client{Timeout: 8 * time.Second}
		u := "https://fr.wiktionary.org/w/api.php?action=parse&prop=wikitext&page=" +
			url.QueryEscape(word) + "&format=json&formatversion=2&redirects=1"
		req, _ := http.NewRequest("GET", u, nil)
		req.Header.Set("User-Agent", "motus-kawaii/1.0 (jeu terminal)")
		resp, err := client.Do(req)
		if err != nil {
			return defMsg("")
		}
		defer resp.Body.Close()
		var raw struct {
			Parse struct {
				Wikitext string `json:"wikitext"`
			} `json:"parse"`
		}
		body, _ := io.ReadAll(resp.Body)
		json.Unmarshal(body, &raw)
		lines := strings.Split(raw.Parse.Wikitext, "\n")
		var out []string
		in := false
		for _, l := range lines {
			if strings.HasPrefix(l, "== {{langue|fr}} ==") {
				in = true
				continue
			}
			if in && strings.HasPrefix(l, "== {{") && !strings.HasPrefix(l, "===") {
				break
			}
			if in && strings.HasPrefix(l, "# ") {
				d := l[2:]
				for _, r := range d {
					if !unicode.IsPrint(r) {
					}
				}
				out = append(out, "# "+d)
			}
		}
		return defMsg(cleanWiki(strings.Join(out, "\n")))
	}
}

func cleanWiki(s string) string {
	s = strings.ReplaceAll(s, "'''", "")
	s = strings.ReplaceAll(s, "''", "")
	open := strings.Index(s, "{{")
	for open >= 0 {
		closeI := strings.Index(s[open:], "}}")
		if closeI < 0 {
			break
		}
		s = s[:open] + s[open+closeI+2:]
		open = strings.Index(s, "{{")
	}
	open = strings.Index(s, "[[")
	for open >= 0 {
		closeI := strings.Index(s[open:], "]]")
		if closeI < 0 {
			break
		}
		inner := s[open+2 : open+closeI]
		if p := strings.Index(inner, "|"); p >= 0 {
			inner = inner[p+1:]
		}
		s = s[:open] + inner + s[open+closeI+2:]
		open = strings.Index(s, "[[")
	}
	return s
}

var defCmds []tea.Cmd

func (m *model) submit() {
	if m.over || m.cursor < m.len {
		return
	}
	word := string(m.current)
	found := false
	for _, w := range dict[m.len] {
		if w == word {
			found = true
			break
		}
	}
	if !found {
		m.message = word + " n'est pas dans mon dictionnaire... (＞﹏＜"
		return
	}
	fb, locked := score(word, m.secret)
	m.grid = append(m.grid, append([]rune(nil), m.current...))
	m.states = append(m.states, []byte(fb))
	if word == m.secret {
		m.over, m.won = true, true
		m.message = fmt.Sprintf("GAGNÉ ! %d essai(s) d'avance ☆*:.｡.o(≧▽≦)o.｡.:*☆", maxTries-len(m.grid))
		defCmds = append(defCmds, fetchDef(m.secret))
		return
	}
	if len(m.grid) >= maxTries {
		m.over = true
		m.message = "Perdu ! Le mot était " + strings.ToUpper(m.secret) + " (´；ω；`)"
		defCmds = append(defCmds, fetchDef(m.secret))
		return
	}
	m.locked = locked
	m.current = make([]rune, m.len)
	for i := 0; i < m.len; i++ {
		if locked[i] {
			m.current[i] = rune(m.secret[i])
		}
	}
	m.cursor = firstFree(m, 1)
	m.message = "Raté ! Une lettre en plus... ♪"
}

func cell(ch rune, st lipgloss.Style, showDot bool) string {
	if ch == 0 {
		if showDot {
			return st.Render("  ·  ")
		}
		return "     "
	}
	return st.Render("  " + string(ch) + "  ")
}

func hline(l, mid, r string, n int) string {
	seg := strings.Repeat("─", 5)
	return l + strings.TrimSuffix(strings.Repeat(seg+mid, n), mid) + r
}

func (m *model) renderGrid() string {
	var b strings.Builder
	n := m.len
	b.WriteString(hline("┌", "┬", "┐", n) + "\n")
	for r := 0; r < maxTries; r++ {
		b.WriteString("│")
		for c := 0; c < n; c++ {
			var ch rune
			var st lipgloss.Style
			if r < len(m.grid) {
				ch = m.grid[r][c]
				switch m.states[r][c] {
				case 'G':
					st = goodStyle
				case 'P':
					st = presStyle
				default:
					st = missStyle
				}
			} else if r == len(m.grid) {
				ch = m.current[c]
				switch {
				case m.locked[c]:
					st = givenStyle
				case c == m.cursor && !m.over:
					st = cursorSty
					ch = '_'
				default:
					st = missStyle
				}
			} else {
				st = missStyle
			}
			showDot := ch == 0
			b.WriteString(cell(ch, st, showDot) + "│")
		}
		b.WriteString("\n")
		if r < maxTries-1 {
			b.WriteString(hline("├", "┼", "┤", n) + "\n")
		}
	}
	b.WriteString(hline("└", "┴", "┘", n))
	return b.String()
}

func centerLines(w, h int, lines []string) string {
	width := func(s string) int {
		p := strings.ReplaceAll(s, "\x1b", "")
		// strip ANSI
		var out strings.Builder
		i := 0
		for i < len(p) {
			if p[i] == '[' && i > 0 {
				// handled below
			}
			out.WriteByte(p[i])
			i++
		}
		return lipgloss.Width(s)
	}
	maxw := 0
	for _, l := range lines {
		if lw := lipgloss.Width(l); lw > maxw {
			maxw = lw
		}
	}
	_ = width
	var b strings.Builder
	vpad := (h - len(lines)) / 2
	if vpad < 0 {
		vpad = 0
	}
	for i := 0; i < vpad; i++ {
		b.WriteString("\n")
	}
	for _, l := range lines {
		lw := lipgloss.Width(l)
		pad := (maxw - lw) / 2
		outer := (w - maxw) / 2
		if outer < 0 {
			outer = 0
		}
		b.WriteString(strings.Repeat(" ", outer+pad) + l + "\n")
	}
	return b.String()
}

func (m *model) View() string {
	if m.menu {
		lines := []string{titleStyle.Render(" ♡  M O T U S  ♡ "), ""}
		lines = append(lines, msgStyle.Render("Choisis ta longueur :"), "")
		for i, l := range levels {
			prefix := fmt.Sprintf("  [%d] ", i+1)
			line := fmt.Sprintf("%d lettres · %d essais", l.letters, maxTries)
			style := msgStyle
			if i == m.level {
				style = cursorSty
			}
			lines = append(lines, style.Render(prefix+line))
		}
		lines = append(lines, "", helpStyle.Render("↑↓ choisir · entrée jouer · 1-5 direct · q quitter"))
		return centerLines(m.width, m.height, lines)
	}
	var lines []string
	if m.over {
		lines = append(lines, titleStyle.Render("♡  M O T U S  ♡"), "", m.renderGrid(), "",
			msgStyle.Render(m.message), "")
		if strings.TrimSpace(m.def) != "" {
			lines = append(lines, msgStyle.Render("📖 Définition"))
			for _, l := range strings.Split(m.def, "\n") {
				l = strings.TrimSpace(l)
				l = strings.TrimPrefix(l, "# ")
				if l != "" {
					lines = append(lines, msgStyle.Render(l))
				}
			}
		}
		lines = append(lines, "", helpStyle.Render("[n] rejouer · esc menu · q quitter"))
	} else {
		lines = append(lines, titleStyle.Render("♡  M O T U S  ♡"), "",
			fmt.Sprintf("mot de %d lettres · essai %d/%d", m.len, len(m.grid)+1, maxTries),
			"", m.renderGrid(), "", msgStyle.Render(m.message), "",
			helpStyle.Render("←→ bouger · backspace · entrée · esc menu"))
	}
	return centerLines(m.width, m.height, lines)
}

func init() {
	rand.Seed(time.Now().UnixNano())
}

func main() {
	loadData()
	m := &model{menu: true, level: 0}
	if _, err := tea.NewProgram(m, tea.WithAltScreen()).Run(); err != nil {
		fmt.Println("erreur:", err)
		os.Exit(1)
	}
}
