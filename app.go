package main

import (
	"bufio"
	"fmt"
	"math"
	"os/exec"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
)

// commitInfo holds the information for a single commit
type commitInfo struct {
	hash       string
	message    string
	author     string
	date       time.Time
	diffLoaded bool

	// These are the diff stats for this specific commit
	files     int
	additions int
	deletions int
	churn     int

	// These are the cumulative stats up to this this commit
	cumulativeFiles     int
	cumulativeAdditions int
	cumulativeDeletions int
}

type authorStat struct {
	name  string
	churn int
}

// Model represents the Bubble Tea application model
type Model struct {
	config             Config
	commits            []*commitInfo
	currentCommitIndex int
	width, height      int // Terminal dimensions
	networkGraphHeight int
	graphColumns       int
	maxAdditions       int
	maxDeletions       int

	autoProgress     bool
	progressInterval time.Duration

	processedCommitsChan chan *commitInfo
	loadingComplete      bool
	program              *tea.Program
}

func (m *Model) SetProgram(p *tea.Program) {
	m.program = p
}

func InitialModel(cfg Config) Model {
	return Model{
		config:               cfg,
		currentCommitIndex:   0,
		autoProgress:         cfg.AutoProgress,
		progressInterval:     time.Duration(cfg.ProgressIntervalMs) * time.Millisecond,
		networkGraphHeight:   0,
		graphColumns:         0,
		maxAdditions:         0,
		maxDeletions:         0,
		loadingComplete:      false,
		processedCommitsChan: make(chan *commitInfo, 100),
	}
}

func (m *Model) Init() tea.Cmd {
	go m.fetcher()
	return m.progressTickCmd()
}

func (m *Model) fetcher() {
	defer close(m.processedCommitsChan)

	r, err := git.PlainOpenWithOptions(m.config.RepoPath, &git.PlainOpenOptions{EnableDotGitCommonDir: true})
	if err != nil {
		if m.program != nil {
			m.program.Send(errMsg{fmt.Errorf("failed to open repository: %v", err)})
		}
		return
	}

	cmd := exec.Command("git", "-C", m.config.RepoPath, "rev-list", "--reverse", "HEAD")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		if m.program != nil {
			m.program.Send(errMsg{fmt.Errorf("failed to create stdout pipe for git rev-list: %v", err)})
		}
		return
	}

	if err := cmd.Start(); err != nil {
		if m.program != nil {
			m.program.Send(errMsg{fmt.Errorf("failed to start git rev-list: %v", err)})
		}
		return
	}

	scanner := bufio.NewScanner(stdout)
	commitCount := 0

	for scanner.Scan() {
		hashStr := scanner.Text()
		hash := plumbing.NewHash(hashStr)

		commit, err := r.CommitObject(hash)
		if err != nil {
			continue
		}

		var filesChanged, additions, deletions, churn int
		if commit.NumParents() > 0 {
			parent, err := commit.Parent(0)
			if err != nil {
				continue
			}
			cTree, err := commit.Tree()
			if err != nil {
				continue
			}
			pTree, err := parent.Tree()
			if err != nil {
				continue
			}
			patch, err := pTree.Patch(cTree)
			if err != nil {
				continue
			}
			stats := patch.Stats()
			filesChanged = len(stats)
			for _, s := range stats {
				additions += s.Addition
				deletions += s.Deletion
			}
			churn = additions + deletions
		}

		m.processedCommitsChan <- &commitInfo{
			hash:      commit.Hash.String(),
			message:   commit.Message,
			author:    commit.Author.Name,
			date:      commit.Author.When,
			files:     filesChanged,
			additions: additions,
			deletions: deletions,
			churn:     churn,
		}
		commitCount++
		if m.config.CommitLimit > 0 && commitCount >= m.config.CommitLimit {
			break
		}
	}

	cmd.Wait()
}

type progressTickMsg time.Time

func (m *Model) progressTickCmd() tea.Cmd {
	return tea.Tick(m.progressInterval, func(t time.Time) tea.Msg {
		return progressTickMsg(t)
	})
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "right", "l":
			m.autoProgress = false
			if m.currentCommitIndex < len(m.commits)-1 {
				m.currentCommitIndex++
			}
			return m, nil
		case "left", "h":
			m.autoProgress = false
			if m.currentCommitIndex > 0 {
				m.currentCommitIndex--
			}
			return m, nil
		case "p": // Toggle auto-progression
			m.autoProgress = !m.autoProgress
			return m, nil
		}
	case tea.WindowSizeMsg:
		m.width = msg.Width - 10
		m.height = msg.Height - 10
		m.graphColumns = m.width/2 - 10
		m.networkGraphHeight = m.height/3 - 10

	case progressTickMsg:
		if m.autoProgress {
			select {
			case newCommit, ok := <-m.processedCommitsChan:
				if ok {
					// Atomically process the new commit and update the index
					newCommit.diffLoaded = true

					if len(m.commits) > 0 {
						lastCommit := m.commits[len(m.commits)-1]
						newCommit.cumulativeFiles = lastCommit.cumulativeFiles + newCommit.files
						newCommit.cumulativeAdditions = lastCommit.cumulativeAdditions + newCommit.additions
						newCommit.cumulativeDeletions = lastCommit.cumulativeDeletions + newCommit.deletions
					} else {
						newCommit.cumulativeFiles = newCommit.files
						newCommit.cumulativeAdditions = newCommit.additions
						newCommit.cumulativeDeletions = newCommit.deletions
					}

					if newCommit.additions > m.maxAdditions {
						m.maxAdditions = newCommit.additions
					}
					if newCommit.deletions > m.maxDeletions {
						m.maxDeletions = newCommit.deletions
					}

					m.commits = append(m.commits, newCommit)
					m.currentCommitIndex = len(m.commits) - 1

				} else {
					m.loadingComplete = true
				}
			default:
				// Channel is empty for now, but not closed.
			}
		}
		return m, m.progressTickCmd()

	case errMsg:
		return m, tea.Quit
	}
	return m, nil
}

// --- Lipgloss Styles ---
var (
	panelStyle         = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("239")).Padding(0, 1)
	headerStyle        = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("147")).Padding(0, 1).Align(lipgloss.Center)
	statsLabelStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("245")).Align(lipgloss.Right).Width(12)
	statsValueStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("117")).Bold(true).Align(lipgloss.Left).Width(12)

	barChar           = "█"
	barStyle          = lipgloss.NewStyle().Foreground(lipgloss.Color("75"))
	barLabelStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Width(8).Align(lipgloss.Right)
	barValueStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("245")).Align(lipgloss.Left).Width(7)
	barMessageStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("247")).Align(lipgloss.Left)
	barHighlightStyle = lipgloss.NewStyle().Background(lipgloss.Color("236"))

	additionStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("118")) // Bright green
	deletionStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("203")) // Bright red
	graphAxisStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("238"))
	graphHighlight = lipgloss.NewStyle().Foreground(lipgloss.Color("255")).Bold(true)
)

func (m *Model) renderPanelWithHeader(title string, content string, width int, height int) string {
	panel := lipgloss.NewStyle().
		Width(width).
		Height(height).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("239"))

	header := lipgloss.NewStyle().
		Width(width - 2).
		Align(lipgloss.Center).
		Bold(true).
		Foreground(lipgloss.Color("147")).
		Render("[ " + title + " ]")

	contentArea := lipgloss.NewStyle().
		Width(width - 4).
		Height(height - 2).
		Render(content)

	fullContent := lipgloss.JoinVertical(lipgloss.Left, header, contentArea)

	return panel.Render(fullContent)
}

func (m *Model) renderCombinedChangesGraph(height int) string {
	if height < 10 {
		return "Not enough space"
	}

	locGraphHeight := height
	locGraph := m.renderLOCGraph(locGraphHeight)

	return locGraph
}

func (m *Model) View() string {
	if len(m.commits) == 0 {
		return "Loading commits..."
	}

	if m.currentCommitIndex >= len(m.commits) {
		m.currentCommitIndex = len(m.commits) - 1
	}
	currentCommit := m.commits[m.currentCommitIndex]

	// Calculate author count dynamically
	authorSet := make(map[string]struct{})
	for i := 0; i <= m.currentCommitIndex; i++ {
		authorSet[m.commits[i].author] = struct{}{}
	}

	statsBuilder := strings.Builder{}

	statsBuilder.WriteString(fmt.Sprintf("  Author: %s\n", currentCommit.author))
	statsBuilder.WriteString(fmt.Sprintf("  Date: %s\n", currentCommit.date.Format("2006-01-02 15:04")))
	statsBuilder.WriteString("\n")
	statsBuilder.WriteString(fmt.Sprintf("%s%s\n",
		statsLabelStyle.Render("Commits:"),
		statsValueStyle.Render(fmt.Sprintf("%d", m.currentCommitIndex+1))))
	statsBuilder.WriteString(fmt.Sprintf("%s%s\n",
		statsLabelStyle.Render("Authors:"),
		statsValueStyle.Render(fmt.Sprintf("%d", len(authorSet)))))

	statsBuilder.WriteString(fmt.Sprintf("%s%s\n",
		statsLabelStyle.Render("Additions:"),
		statsValueStyle.Render(fmt.Sprintf("+%d", currentCommit.cumulativeAdditions))))
	statsBuilder.WriteString(fmt.Sprintf("%s%s\n",
		statsLabelStyle.Render("Deletions:"),
		statsValueStyle.Render(fmt.Sprintf("-%d", currentCommit.cumulativeDeletions))))

	statsPanelHeight := 8
	changesPanelHeight := m.height*2/3 - 10
	timelinePanelHeight := m.height - statsPanelHeight - changesPanelHeight
	if timelinePanelHeight < 8 {
		timelinePanelHeight = 8
		changesPanelHeight = m.height - statsPanelHeight - timelinePanelHeight
	}

	barChartContent := m.renderTimeline(timelinePanelHeight - 3)
	combinedGraphContent := m.renderCombinedChangesGraph(changesPanelHeight - 3)

	leftColumn := lipgloss.JoinVertical(lipgloss.Left,
		m.renderPanelWithHeader("Commit & Project Stats", statsBuilder.String(), m.width/2-2, statsPanelHeight),
		m.renderPanelWithHeader("Commit Changes", combinedGraphContent, m.width/2-2, changesPanelHeight),
		m.renderPanelWithHeader("Commit Timeline", barChartContent, m.width/2-2, timelinePanelHeight),
	)

	rightColumn := m.renderPanelWithHeader("Developer Stats", m.renderDeveloperStats(), m.width/2-2, m.height)

	return lipgloss.JoinHorizontal(lipgloss.Top, leftColumn, rightColumn)
}

func (m *Model) renderTimeline(timelineHeight int) string {
	if len(m.commits) == 0 {
		return "No commits"
	}
	if timelineHeight <= 0 {
		return "Not enough space"
	}

	// Try to center the current commit index
	visibleStart := m.currentCommitIndex - timelineHeight/2
	if visibleStart < 0 {
		visibleStart = 0
	}

	// Adjust start to ensure the panel is full, if possible
	if visibleStart+timelineHeight > len(m.commits) {
		visibleStart = len(m.commits) - timelineHeight
		if visibleStart < 0 {
			visibleStart = 0
		}
	}

	visibleEnd := visibleStart + timelineHeight
	if visibleEnd > len(m.commits) {
		visibleEnd = len(m.commits)
	}

	barChartContent := strings.Builder{}

	labelWidth := 8
	statsWidth := 15
	padding := 2
	availableWidth := m.width/2 - 6
	msgWidth := availableWidth - labelWidth - statsWidth - padding
	if msgWidth < 20 {
		msgWidth = 20
	}

	for i := visibleStart; i < visibleEnd; i++ {
		c := m.commits[i]

		label := barLabelStyle.Render(c.hash[:7])

		var stats string
		addFormatted := "+" + formatStat(c.additions)
		delFormatted := "-" + formatStat(c.deletions)
		addStr := lipgloss.NewStyle().Width(7).Align(lipgloss.Left).Render(additionStyle.Render(addFormatted))
		delStr := lipgloss.NewStyle().Width(7).Align(lipgloss.Left).Render(deletionStyle.Render(delFormatted))
		stats = lipgloss.JoinHorizontal(lipgloss.Left, addStr, " ", delStr)

		msg := truncateMessage(c.message, msgWidth)
		if i == m.currentCommitIndex {
			msg = lipgloss.NewStyle().Foreground(lipgloss.Color("255")).Bold(true).Render(msg)
		} else {
			msg = barMessageStyle.Render(msg)
		}

		line := fmt.Sprintf("%s %s %s", label, stats, msg)
		if i == m.currentCommitIndex {
			line = barHighlightStyle.Render(line)
		}
		barChartContent.WriteString(line + "\n")
	}

	return barChartContent.String()
}

func (m *Model) renderLOCGraph(graphHeight int) string {
	if len(m.commits) == 0 || m.graphColumns <= 10 {
		return "Insufficient data"
	}
	if graphHeight < 5 {
		graphHeight = 5
	}

	windowSize := m.graphColumns
	displayCommits := m.commits[:m.currentCommitIndex+1]

	startIndex := max(0, len(displayCommits)-windowSize)
	endIndex := len(displayCommits)

	maxVal := max(m.maxAdditions, m.maxDeletions)
	if maxVal < 1 {
		maxVal = 1
	}

	grid := make([][]string, graphHeight)
	for i := range grid {
		grid[i] = make([]string, m.graphColumns)
		for j := range grid[i] {
			grid[i][j] = " "
		}
	}

	zeroLine := graphHeight / 2
	for col := 0; col < m.graphColumns; col++ {
		grid[zeroLine][col] = graphAxisStyle.Render("─")
	}

	for i := startIndex; i < endIndex; i++ {
		c := displayCommits[i]
		col := m.graphColumns - (endIndex - i)

		if col < 0 || col >= m.graphColumns {
			continue
		}

		logMaxAdd := math.Log1p(float64(m.maxAdditions))
		if logMaxAdd == 0 {
			logMaxAdd = 1
		}
		logMaxDel := math.Log1p(float64(m.maxDeletions))
		if logMaxDel == 0 {
			logMaxDel = 1
		}

		scaledAdditions := 0
		if c.additions > 0 {
			scaledAdditions = int((math.Log1p(float64(c.additions)) / logMaxAdd) * float64(zeroLine-1))
		}
		scaledDeletions := 0
		if c.deletions > 0 {
			scaledDeletions = int((math.Log1p(float64(c.deletions)) / logMaxDel) * float64(zeroLine-1))
		}

		isCurrentCommit := (i == m.currentCommitIndex)

		for y := 1; y <= scaledAdditions && y < zeroLine; y++ {
			row := zeroLine - y
			if isCurrentCommit {
				grid[row][col] = graphHighlight.Render("█")
			} else {
				grid[row][col] = additionStyle.Render("█")
			}
		}

		for y := 1; y <= scaledDeletions && zeroLine+y < graphHeight; y++ {
			row := zeroLine + y
			if isCurrentCommit {
				grid[row][col] = graphHighlight.Render("█")
			} else {
				grid[row][col] = deletionStyle.Render("█")
			}
		}

		if isCurrentCommit {
			grid[zeroLine][col] = graphHighlight.Render("◆")
		}
	}

	result := strings.Builder{}
	for row := 0; row < graphHeight; row++ {
		result.WriteString(strings.Join(grid[row], ""))
		result.WriteString("\n")
	}
	return result.String()
}

func (m *Model) renderDeveloperStats() string {
	// Calculate stats dynamically based on the current index
	authorChurn := make(map[string]int)
	weekdayCounts := make(map[time.Weekday]int)
	monthCounts := make(map[time.Month]int)
	hourCounts := make(map[int]int)

	for i := 0; i <= m.currentCommitIndex; i++ {
		c := m.commits[i]
		authorChurn[c.author] += c.churn
		weekdayCounts[c.date.Weekday()]++
		monthCounts[c.date.Month()]++
		hourCounts[c.date.Local().Hour()]++
	}

	// Determine top contributors
	topContributors := make([]authorStat, 0, len(authorChurn))
	for name, churn := range authorChurn {
		topContributors = append(topContributors, authorStat{name: name, churn: churn})
	}
	sort.Slice(topContributors, func(i, j int) bool {
		return topContributors[i].churn > topContributors[j].churn
	})

	var b strings.Builder
	availableWidth := m.width/2 - 8

	barChartWidth := availableWidth - 20
	if barChartWidth < 10 {
		barChartWidth = 10
	}

	b.WriteString(headerStyle.Render("Top 5 Contributors (by churn)"))
	b.WriteString("\n")
	for i := 0; i < len(topContributors) && i < 5; i++ {
		b.WriteString(fmt.Sprintf(" %-18s %d\n", truncateMessage(topContributors[i].name, 32), topContributors[i].churn))
	}
	b.WriteString("\n")

	b.WriteString(headerStyle.Render("Commits by Month"))
	b.WriteString("\n")
	months := []time.Month{time.January, time.February, time.March, time.April, time.May, time.June, time.July, time.August, time.September, time.October, time.November, time.December}
	maxMonthCount := 0
	for _, month := range months {
		if count := monthCounts[month]; count > maxMonthCount {
			maxMonthCount = count
		}
	}
	if maxMonthCount == 0 {
		maxMonthCount = 1
	}
	for _, month := range months {
		count := monthCounts[month]
		barLength := (count * barChartWidth) / maxMonthCount
		bar := strings.Repeat(barChar, barLength)
		b.WriteString(fmt.Sprintf(" %-12s |%s %-5d\n", month.String(), barStyle.Render(bar), count))
	}
	b.WriteString("\n")

	b.WriteString(headerStyle.Render("Commits by Weekday"))
	b.WriteString("\n")
	weekdays := []time.Weekday{time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday, time.Saturday, time.Sunday}
	maxWeekdayCount := 0
	for _, day := range weekdays {
		if count := weekdayCounts[day]; count > maxWeekdayCount {
			maxWeekdayCount = count
		}
	}
	if maxWeekdayCount == 0 {
		maxWeekdayCount = 1
	}
	for _, day := range weekdays {
		count := weekdayCounts[day]
		barLength := (count * barChartWidth) / maxWeekdayCount
		bar := strings.Repeat(barChar, barLength)
		b.WriteString(fmt.Sprintf(" %-12s |%s %-5d\n", day.String(), barStyle.Render(bar), count))
	}
	b.WriteString("\n")

	b.WriteString(headerStyle.Render("Commits by Hour (Local)"))
	b.WriteString("\n")
	maxHourCount := 0
	for i := 0; i < 24; i++ {
		if count := hourCounts[i]; count > maxHourCount {
			maxHourCount = count
		}
	}
	if maxHourCount == 0 {
		maxHourCount = 1
	}
	for i := 0; i < 24; i++ {
		count := hourCounts[i]
		hourLabel := fmt.Sprintf("%02d:00-%02d:59", i, i)
		barLength := (count * barChartWidth) / maxHourCount
		bar := strings.Repeat(barChar, barLength)
		b.WriteString(fmt.Sprintf(" %-12s |%s %-5d\n", hourLabel, barStyle.Render(bar), count))
	}

	return b.String()
}

// Helper functions
func truncateMessage(msg string, maxLen int) string {
	lines := strings.Split(msg, "\n")
	result := lines[0]
	if len(result) > maxLen {
		return result[:maxLen-3] + "..."
	}
	return result
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func formatStat(n int) string {
	absN := n
	if absN < 0 {
		absN = -n
	}

	if absN < 1000 {
		return fmt.Sprintf("%d", n)
	}

	f := float64(n)
	sign := ""
	if n < 0 {
		f = -f
		sign = "-"
	}

	if absN < 1000000 {
		return fmt.Sprintf("%s%.1fk", sign, f/1000)
	}
	if absN < 1000000000 {
		return fmt.Sprintf("%s%.1fM", sign, f/1000000)
	}
	return fmt.Sprintf("%s%.1fG", sign, f/1000000000)
}

type errMsg struct{ err error }

func (e errMsg) Error() string { return e.err.Error() }