package main

import (
	"context"
	"fmt"
	"image/color"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/data/binding"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/TipoKrewaz/scadt/internal/config"
	"github.com/TipoKrewaz/scadt/internal/models"
	"github.com/TipoKrewaz/scadt/internal/runner"
)


var (
	colBG     = color.NRGBA{R: 0x0a, G: 0x0a, B: 0x0a, A: 0xff}
	colPanel  = color.NRGBA{R: 0x11, G: 0x11, B: 0x11, A: 0xff}
	colCard   = color.NRGBA{R: 0x16, G: 0x16, B: 0x16, A: 0xff}
	colBorder = color.NRGBA{R: 0x2a, G: 0x2a, B: 0x2a, A: 0xff}
	colText   = color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}
	colDim    = color.NRGBA{R: 0x88, G: 0x88, B: 0x88, A: 0xff}
	colMuted  = color.NRGBA{R: 0x55, G: 0x55, B: 0x55, A: 0xff}
	colAccent = color.NRGBA{R: 0xff, G: 0x44, B: 0x44, A: 0xff}
	colOK     = color.NRGBA{R: 0x00, G: 0xff, B: 0x88, A: 0xff}
	colWarn   = color.NRGBA{R: 0xff, G: 0xb0, B: 0x20, A: 0xff}
	colInfo   = color.NRGBA{R: 0x5a, G: 0xa8, B: 0xff, A: 0xff}
	colScarPk = color.NRGBA{R: 0xff, G: 0x88, B: 0xc8, A: 0xff}
)

type scadtTheme struct{}

func (scadtTheme) Color(n fyne.ThemeColorName, v fyne.ThemeVariant) color.Color {
	switch n {
	case theme.ColorNameBackground:
		return colBG
	case theme.ColorNameForeground:
		return colText
	case theme.ColorNameButton, theme.ColorNameInputBackground:
		return colCard
	case theme.ColorNameHover:
		return colBorder
	case theme.ColorNamePressed:
		return colBorder
	case theme.ColorNameDisabled:
		return colMuted
	case theme.ColorNamePlaceHolder, theme.ColorNameDisabledButton:
		return colDim
	case theme.ColorNamePrimary, theme.ColorNameFocus:
		return colAccent
	case theme.ColorNameSelection:
		return color.NRGBA{R: 0xff, G: 0x44, B: 0x44, A: 0x44}
	case theme.ColorNameScrollBar, theme.ColorNameSeparator, theme.ColorNameInputBorder:
		return colBorder
	case theme.ColorNameShadow:
		return color.NRGBA{A: 0x80}
	case theme.ColorNameError:
		return colAccent
	case theme.ColorNameSuccess:
		return colOK
	case theme.ColorNameWarning:
		return colWarn
	}
	return theme.DefaultTheme().Color(n, theme.VariantDark)
}

func (scadtTheme) Icon(n fyne.ThemeIconName) fyne.Resource { return theme.DefaultTheme().Icon(n) }
func (scadtTheme) Font(s fyne.TextStyle) fyne.Resource     { return theme.DefaultTheme().Font(s) }
func (scadtTheme) Size(n fyne.ThemeSizeName) float32 {
	switch n {
	case theme.SizeNamePadding, theme.SizeNameInnerPadding:
		return 6
	case theme.SizeNameText:
		return 13
	case theme.SizeNameCaptionText:
		return 11
	case theme.SizeNameInputBorder:
		return 1
	}
	return theme.DefaultTheme().Size(n)
}


type backend struct {
	cfg    *config.Store
	store  *Store
	hub    *Hub
	dm     *DriverManager
	alerts *AlertEngine
}


type gui struct {
	be  *backend
	app fyne.App
	win fyne.Window

	activeServer string
	paused       bool
	total        int
	rateStamps   []time.Time

	events []models.Event
	evMu   sync.RWMutex

	// widgets (live-refresh)
	serverList        *widget.List
	sidebarEmptyBox   *fyne.Container
	sidebarListStack  *fyne.Container
	serverCount       *widget.Label
	connBadge         *canvas.Text
	radarTable        *widget.Table
	radarServerSelect *widget.Select
	metricRate        *widget.Label
	metricTotal       *widget.Label
	statsText         *widget.Entry
	savedList         *widget.List
	rulesList         *widget.List
	historyList       *widget.List
	runnerOutput      *widget.Entry
	runnerSrv         *widget.Select
	reqMethod         *widget.Select
	reqPath           *widget.Entry
	reqHeaders        map[string]string
	respLabel         *widget.Label
	respBody          *widget.Entry
	respBox           *fyne.Container

	filterBox binding.String
	levelBox  binding.String
	serverBox binding.String
}

func runGUI(be *backend) {
	g := &gui{
		be:         be,
		reqHeaders: map[string]string{},
		filterBox:  binding.NewString(),
		levelBox:   binding.NewString(),
		serverBox:  binding.NewString(),
	}

	if os.Getenv("FYNE_SCALE") == "" {
		_ = os.Setenv("FYNE_SCALE", "1.1")
	}

	a := app.NewWithID("com.scarlett.scadt")
	a.Settings().SetTheme(&scadtTheme{})
	g.app = a

	w := a.NewWindow("ScaDT")
	w.Resize(detectWindowSize())
	w.CenterOnScreen()
	g.win = w

	w.SetContent(g.buildRoot())
	g.refreshAll()
	go g.consumeEvents()
	go g.startTickers()

	w.ShowAndRun()
}

func detectWindowSize() fyne.Size {
	if s := os.Getenv("SCADT_WINDOW"); s != "" {
		var w, h int
		if _, err := fmt.Sscanf(s, "%dx%d", &w, &h); err == nil && w > 400 && h > 300 {
			return fyne.NewSize(float32(w), float32(h))
		}
	}
	return fyne.NewSize(1600, 1000)
}


func (g *gui) buildRoot() fyne.CanvasObject {
	header := g.buildHeader()
	sidebar := g.buildSidebar()
	content := g.buildContent()
	split := container.NewHSplit(sidebar, content)
	split.SetOffset(0.18)
	return container.NewBorder(header, nil, nil, nil, split)
}


func (g *gui) buildHeader() fyne.CanvasObject {
	brand := canvas.NewText("● ScaDT · Debug Unit", colAccent)
	brand.TextStyle = fyne.TextStyle{Bold: true}
	brand.TextSize = 14

	title := canvas.NewText("Консоль управления", colText)
	title.TextStyle = fyne.TextStyle{Bold: true}
	title.TextSize = 18

	badge := canvas.NewText("подключаемся…", colDim)
	badge.TextStyle = fyne.TextStyle{Monospace: true}
	badge.TextSize = 11
	g.connBadge = badge

	paletteBtn := widget.NewButtonWithIcon("Ctrl+K", theme.SearchIcon(), func() { g.openPalette() })
	settingsBtn := widget.NewButtonWithIcon("Settings", theme.SettingsIcon(), func() { g.openSettings() })
	monitorBtn := widget.NewButtonWithIcon("⏸ Пауза", theme.MediaPauseIcon(), nil)
	monitorBtn.OnTapped = func() {
		g.togglePause()
		if g.paused {
			monitorBtn.SetText("▶ Продолжить")
			monitorBtn.SetIcon(theme.MediaPlayIcon())
		} else {
			monitorBtn.SetText("⏸ Пауза")
			monitorBtn.SetIcon(theme.MediaPauseIcon())
		}
	}
	monitorBtn.Importance = widget.HighImportance

	left := container.NewHBox(brand, widget.NewLabel(" "), title, widget.NewLabel(" "), badge)
	right := container.NewHBox(paletteBtn, settingsBtn, monitorBtn)

	bg := canvas.NewRectangle(colPanel)
	sep := canvas.NewRectangle(colBorder)
	sep.SetMinSize(fyne.NewSize(0, 1))
	row := container.NewBorder(nil, nil, left, right)
	return container.NewStack(bg, container.NewBorder(nil, sep, nil, nil, container.NewPadded(row)))
}


func (g *gui) buildSidebar() fyne.CanvasObject {
	sec := canvas.NewText("СЕРВЕРЫ", colMuted)
	sec.TextStyle = fyne.TextStyle{Bold: true}
	sec.TextSize = 11

	g.serverCount = widget.NewLabel("—")
	g.serverCount.TextStyle = fyne.TextStyle{Monospace: true}
	secRow := container.NewBorder(nil, nil, sec, g.serverCount)

	g.serverList = widget.NewList(
		func() int { return len(g.be.cfg.Snapshot().Servers) },
		func() fyne.CanvasObject {
			dot := canvas.NewCircle(colMuted)
			dot.Resize(fyne.NewSize(10, 10))
			dotWrap := container.New(&fixedLayout{w: 12, h: 12}, dot)
			name := canvas.NewText("name", colText)
			name.TextStyle = fyne.TextStyle{Bold: true}
			name.TextSize = 13
			sub := canvas.NewText("— · unknown", colMuted)
			sub.TextSize = 10
			sub.TextStyle = fyne.TextStyle{Monospace: true}
			col := container.NewVBox(name, sub)
			return container.NewBorder(nil, nil, dotWrap, nil, col)
		},
		func(i widget.ListItemID, obj fyne.CanvasObject) {
			srvs := g.be.cfg.Snapshot().Servers
			if i >= len(srvs) {
				return
			}
			s := srvs[i]
			border := obj.(*fyne.Container)
			// NewBorder(nil,nil,dotWrap,nil, col) → Objects = [col, dotWrap]
			col := border.Objects[0].(*fyne.Container)
			dotWrap := border.Objects[1].(*fyne.Container)
			dot := dotWrap.Objects[0].(*canvas.Circle)
			stateCls := "offline"
			if s.Status.State != "" {
				stateCls = s.Status.State
			}
			switch stateCls {
			case "online":
				dot.FillColor = colOK
			case "degraded":
				dot.FillColor = colWarn
			case "unknown":
				dot.FillColor = colInfo
			default:
				dot.FillColor = colMuted
			}
			dot.Refresh()
			name := col.Objects[0].(*canvas.Text)
			sub := col.Objects[1].(*canvas.Text)
			name.Text = s.Name
			ping := "—"
			if s.Status.Ping > 0 {
				ping = strconv.Itoa(s.Status.Ping) + "ms"
			}
			sub.Text = ping + " · " + stateCls
			name.Refresh()
			sub.Refresh()
		},
	)
	g.serverList.OnSelected = func(id widget.ListItemID) {
		srvs := g.be.cfg.Snapshot().Servers
		if id < len(srvs) {
			g.activeServer = srvs[id].Name
		}
	}

	addBtn := widget.NewButtonWithIcon("+ Добавить сервер", theme.ContentAddIcon(), func() {
		g.openServerEditor(nil)
	})
	addBtn.Importance = widget.SuccessImportance

	g.metricRate = widget.NewLabel("0")
	g.metricRate.TextStyle = fyne.TextStyle{Monospace: true, Bold: true}
	g.metricTotal = widget.NewLabel("0")
	g.metricTotal.TextStyle = fyne.TextStyle{Monospace: true, Bold: true}
	metrics := container.NewVBox(
		widget.NewSeparator(),
		container.NewBorder(nil, nil, smallCap("ERRORS/MIN"), g.metricRate),
		container.NewBorder(nil, nil, smallCap("TOTAL"), g.metricTotal),
	)

	ver := canvas.NewText("ver. 2.0 · Ctrl+K", colMuted)
	ver.TextSize = 10
	ver.TextStyle = fyne.TextStyle{Monospace: true}
	feat := canvas.NewText("feat. ", colDim)
	feat.TextSize = 13
	scar := canvas.NewText("Scarlett ✦", colScarPk)
	scar.TextStyle = fyne.TextStyle{Bold: true, Italic: true}
	scar.TextSize = 15
	featRow := container.NewHBox(feat, scar)
	credits := container.NewVBox(widget.NewSeparator(), ver, featRow)

	bg := canvas.NewRectangle(colPanel)

	emptyHint := canvas.NewText("Серверов нет", colDim)
	emptyHint.TextSize = 13
	emptyHint.TextStyle = fyne.TextStyle{Bold: true}
	emptyHint.Alignment = fyne.TextAlignCenter
	emptyDescr := canvas.NewText("Нажми + чтобы добавить первый", colMuted)
	emptyDescr.TextSize = 11
	emptyDescr.Alignment = fyne.TextAlignCenter
	emptyBox := container.NewCenter(container.NewVBox(emptyHint, emptyDescr))

	// переключатель между списком и empty-state
	listOrEmpty := container.NewStack(g.serverList)
	g.sidebarEmptyBox = emptyBox
	g.sidebarListStack = listOrEmpty
	g.refreshSidebarEmpty()

	body := container.NewBorder(
		container.NewVBox(secRow),
		container.NewVBox(addBtn, metrics, credits),
		nil, nil,
		container.NewStack(listOrEmpty, emptyBox),
	)
	return container.NewStack(bg, container.NewPadded(body))
}

func (g *gui) refreshSidebarEmpty() {
	if g.sidebarEmptyBox == nil || g.sidebarListStack == nil {
		return
	}
	n := len(g.be.cfg.Snapshot().Servers)
	if n == 0 {
		g.sidebarEmptyBox.Show()
		g.sidebarListStack.Hide()
	} else {
		g.sidebarEmptyBox.Hide()
		g.sidebarListStack.Show()
	}
}


func (g *gui) buildContent() fyne.CanvasObject {
	sniper := g.buildSniper()
	tabs := container.NewAppTabs(
		container.NewTabItemWithIcon("Radar", theme.WarningIcon(), g.buildRadar()),
		container.NewTabItemWithIcon("Stats", theme.ListIcon(), g.buildStats()),
		container.NewTabItemWithIcon("Saved", theme.DocumentIcon(), g.buildSaved()),
		container.NewTabItemWithIcon("SSH Runner", theme.ComputerIcon(), g.buildRunner()),
		container.NewTabItemWithIcon("Alerts", theme.VisibilityIcon(), g.buildAlerts()),
	)
	tabs.SetTabLocation(container.TabLocationTop)
	return container.NewBorder(sniper, nil, nil, nil, tabs)
}


func (g *gui) buildSniper() fyne.CanvasObject {
	title := canvas.NewText("SNIPER / DEBUG REQUEST", colMuted)
	title.TextSize = 11
	title.TextStyle = fyne.TextStyle{Bold: true}

	g.reqMethod = widget.NewSelect([]string{"GET", "POST", "PUT", "PATCH", "DELETE"}, nil)
	g.reqMethod.SetSelected("GET")

	g.reqPath = widget.NewEntry()
	g.reqPath.SetPlaceHolder("/api/v2/debug/stacktrace?node_id=fra-1")
	g.reqPath.SetText("/api/status")
	g.reqPath.OnSubmitted = func(string) { g.sendDebug() }

	headersBtn := widget.NewButtonWithIcon("Header", theme.MenuExpandIcon(), g.openHeadersDialog)
	saveBtn := widget.NewButtonWithIcon("★", theme.ContentAddIcon(), g.openSaveRequestDialog)
	sendBtn := widget.NewButtonWithIcon("Отправить →", theme.MailSendIcon(), g.sendDebug)
	sendBtn.Importance = widget.HighImportance

	row := container.NewBorder(nil, nil,
		g.reqMethod,
		container.NewHBox(headersBtn, saveBtn, sendBtn),
		g.reqPath,
	)

	g.respLabel = widget.NewLabel("")
	g.respLabel.TextStyle = fyne.TextStyle{Monospace: true, Bold: true}
	g.respBody = widget.NewMultiLineEntry()
	g.respBody.Wrapping = fyne.TextWrapWord
	g.respBody.Disable()
	respScroll := container.NewScroll(g.respBody)
	respScroll.SetMinSize(fyne.NewSize(0, 90))
	g.respBox = container.NewBorder(g.respLabel, nil, nil, nil, respScroll)
	g.respBox.Hide()

	bg := canvas.NewRectangle(colPanel)
	return container.NewStack(bg, container.NewPadded(container.NewVBox(title, row, g.respBox)))
}


func (g *gui) buildRadar() fyne.CanvasObject {
	filterEntry := widget.NewEntryWithData(g.filterBox)
	filterEntry.SetPlaceHolder("regex / текст")
	filterEntry.OnChanged = func(s string) {
		_ = g.filterBox.Set(s)
		g.refreshRadar()
	}

	levelSel := widget.NewSelect([]string{"", "error", "warn", "info", "debug"}, func(s string) {
		_ = g.levelBox.Set(s)
		g.refreshRadar()
	})
	levelSel.PlaceHolder = "все уровни"

	serverSel := widget.NewSelect(g.serverNamesWithEmpty(), func(s string) {
		_ = g.serverBox.Set(s)
		g.refreshRadar()
	})
	serverSel.PlaceHolder = "все серверы"
	g.radarServerSelect = serverSel

	pauseBtn := widget.NewButton("Пауза", g.togglePause)
	clearBtn := widget.NewButton("Очистить", g.clearRadar)

	toolbar := container.NewBorder(nil, nil,
		container.NewHBox(widget.NewIcon(theme.WarningIcon()), widget.NewLabelWithStyle("Живой поток ошибок", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})),
		container.NewHBox(serverSel, levelSel, pauseBtn, clearBtn),
		filterEntry,
	)

	g.radarTable = widget.NewTable(
		func() (int, int) {
			g.evMu.RLock()
			defer g.evMu.RUnlock()
			return len(g.events) + 1, 4
		},
		func() fyne.CanvasObject {
			lbl := widget.NewLabel("")
			lbl.TextStyle = fyne.TextStyle{Monospace: true}
			return lbl
		},
		func(id widget.TableCellID, obj fyne.CanvasObject) {
			lbl := obj.(*widget.Label)
			if id.Row == 0 {
				lbl.TextStyle = fyne.TextStyle{Bold: true, Monospace: true}
				switch id.Col {
				case 0:
					lbl.SetText("TIME")
				case 1:
					lbl.SetText("SERVER")
				case 2:
					lbl.SetText("SERVICE")
				case 3:
					lbl.SetText("MESSAGE")
				}
				return
			}
			lbl.TextStyle = fyne.TextStyle{Monospace: true}
			g.evMu.RLock()
			defer g.evMu.RUnlock()
			idx := id.Row - 1
			if idx >= len(g.events) {
				lbl.SetText("")
				return
			}
			e := g.events[idx]
			switch id.Col {
			case 0:
				lbl.SetText(e.Timestamp.Format("15:04:05"))
			case 1:
				lbl.SetText(e.Server)
			case 2:
				lbl.SetText(e.Service)
			case 3:
				lbl.SetText(e.Message)
			}
		},
	)
	g.radarTable.SetColumnWidth(0, 80)
	g.radarTable.SetColumnWidth(1, 160)
	g.radarTable.SetColumnWidth(2, 140)
	g.radarTable.SetColumnWidth(3, 900)
	g.radarTable.OnSelected = func(id widget.TableCellID) {
		if id.Row == 0 {
			return
		}
		g.evMu.RLock()
		idx := id.Row - 1
		if idx >= len(g.events) {
			g.evMu.RUnlock()
			return
		}
		e := g.events[idx]
		g.evMu.RUnlock()
		g.openEventDetail(e)
	}

	bg := canvas.NewRectangle(colPanel)
	return container.NewStack(bg, container.NewPadded(container.NewBorder(toolbar, nil, nil, nil, g.radarTable)))
}


func (g *gui) buildStats() fyne.CanvasObject {
	g.statsText = widget.NewMultiLineEntry()
	g.statsText.Wrapping = fyne.TextWrapWord
	g.statsText.TextStyle = fyne.TextStyle{Monospace: true}
	g.statsText.SetText("— нажми «Обновить» —")
	g.statsText.Disable()

	refresh := widget.NewButtonWithIcon("Обновить", theme.ViewRefreshIcon(), g.refreshStats)
	scroll := container.NewScroll(g.statsText)

	bg := canvas.NewRectangle(colPanel)
	return container.NewStack(bg, container.NewPadded(
		container.NewBorder(refresh, nil, nil, nil, scroll),
	))
}


func (g *gui) buildSaved() fyne.CanvasObject {
	addBtn := widget.NewButtonWithIcon("+ Добавить запрос", theme.ContentAddIcon(), g.openSaveRequestDialog)

	g.savedList = widget.NewList(
		func() int { return len(g.be.cfg.Snapshot().SavedRequests) },
		func() fyne.CanvasObject {
			return container.NewBorder(nil, nil,
				widget.NewLabelWithStyle("GET", fyne.TextAlignCenter, fyne.TextStyle{Bold: true}),
				container.NewHBox(
					widget.NewButton("Load", nil),
					widget.NewButton("Run", nil),
					widget.NewButton("×", nil),
				),
				widget.NewLabel(""),
			)
		},
		func(i widget.ListItemID, obj fyne.CanvasObject) {
			reqs := g.be.cfg.Snapshot().SavedRequests
			if i >= len(reqs) {
				return
			}
			r := reqs[i]
			border := obj.(*fyne.Container)
			// NewBorder(nil,nil,method,actions, name) → Objects = [name, method, actions]
			name := border.Objects[0].(*widget.Label)
			method := border.Objects[1].(*widget.Label)
			actions := border.Objects[2].(*fyne.Container)
			method.SetText(r.Method)
			star := ""
			if r.Favorite {
				star = "★ "
			}
			name.SetText(fmt.Sprintf("%s%s  [%s]  %s", star, r.Name, valOr(r.Group, "—"), r.Path))
			idx := i
			actions.Objects[0].(*widget.Button).OnTapped = func() {
				r := g.be.cfg.Snapshot().SavedRequests[idx]
				g.reqMethod.SetSelected(r.Method)
				g.reqPath.SetText(r.Path)
				if r.Server != "" {
					g.activeServer = r.Server
				}
			}
			actions.Objects[1].(*widget.Button).OnTapped = func() {
				r := g.be.cfg.Snapshot().SavedRequests[idx]
				g.reqMethod.SetSelected(r.Method)
				g.reqPath.SetText(r.Path)
				if r.Server != "" {
					g.activeServer = r.Server
				}
				g.reqHeaders = r.Headers
				g.sendDebug()
			}
			actions.Objects[2].(*widget.Button).OnTapped = func() {
				r := g.be.cfg.Snapshot().SavedRequests[idx]
				dialog.ShowConfirm("Удалить", "Удалить '"+r.Name+"'?", func(ok bool) {
					if !ok {
						return
					}
					_ = g.be.cfg.Update(func(f *config.File) {
						if idx < len(f.SavedRequests) {
							f.SavedRequests = append(f.SavedRequests[:idx], f.SavedRequests[idx+1:]...)
						}
					})
					g.savedList.Refresh()
				}, g.win)
			}
		},
	)

	bg := canvas.NewRectangle(colPanel)
	return container.NewStack(bg, container.NewPadded(
		container.NewBorder(addBtn, nil, nil, nil, g.savedList),
	))
}


func (g *gui) buildRunner() fyne.CanvasObject {
	g.runnerSrv = widget.NewSelect(g.sshServerNames(), nil)
	g.runnerSrv.PlaceHolder = "— выбери сервер с SSH config —"

	shortcuts := []string{"uptime", "df -h", "free -m", "systemctl status", "journalctl -n 100 --no-pager", "docker ps"}
	shortRow := container.NewHBox()
	for _, s := range shortcuts {
		cmd := s
		b := widget.NewButton(cmd, func() { g.runSSH(cmd) })
		shortRow.Add(b)
	}

	cmdEntry := widget.NewEntry()
	cmdEntry.SetPlaceHolder("ls -la /var/log")
	cmdEntry.SetText("uptime")
	cmdEntry.OnSubmitted = func(cmd string) { g.runSSH(cmd) }
	execBtn := widget.NewButtonWithIcon("Выполнить", theme.ComputerIcon(), func() { g.runSSH(cmdEntry.Text) })
	execBtn.Importance = widget.HighImportance
	input := container.NewBorder(nil, nil, nil, execBtn, cmdEntry)

	top := container.NewVBox(
		container.NewBorder(nil, nil, widget.NewLabel("Сервер:"), nil, g.runnerSrv),
		shortRow,
		input,
	)

	g.runnerOutput = widget.NewMultiLineEntry()
	g.runnerOutput.Wrapping = fyne.TextWrapWord
	g.runnerOutput.TextStyle = fyne.TextStyle{Monospace: true}
	g.runnerOutput.SetText("готов.")
	g.runnerOutput.Disable()
	out := container.NewScroll(g.runnerOutput)

	bg := canvas.NewRectangle(colPanel)
	return container.NewStack(bg, container.NewPadded(
		container.NewBorder(top, nil, nil, nil, out),
	))
}


func (g *gui) buildAlerts() fyne.CanvasObject {
	addBtn := widget.NewButtonWithIcon("+ Правило", theme.ContentAddIcon(), g.openAlertRuleDialog)

	g.rulesList = widget.NewList(
		func() int { return len(g.be.cfg.Snapshot().AlertRules) },
		func() fyne.CanvasObject {
			return container.NewBorder(nil, nil,
				widget.NewLabelWithStyle("ON", fyne.TextAlignCenter, fyne.TextStyle{Bold: true}),
				container.NewHBox(widget.NewButton("Off", nil), widget.NewButton("×", nil)),
				widget.NewLabel(""),
			)
		},
		func(i widget.ListItemID, obj fyne.CanvasObject) {
			rules := g.be.cfg.Snapshot().AlertRules
			if i >= len(rules) {
				return
			}
			r := rules[i]
			border := obj.(*fyne.Container)
			// NewBorder(nil,nil,state,actions, name) → Objects = [name, state, actions]
			name := border.Objects[0].(*widget.Label)
			state := border.Objects[1].(*widget.Label)
			actions := border.Objects[2].(*fyne.Container)
			if r.Enabled {
				state.SetText("ON")
			} else {
				state.SetText("OFF")
			}
			name.SetText(fmt.Sprintf("%s — ≥%d/%s · level≥%s · svc=%s · srv=%s · → %s",
				r.Name, r.Threshold, valOr(r.Window, "1m"), valOr(r.Level, "*"),
				valOr(r.Service, "*"), valOr(r.Server, "*"), strings.Join(r.Channels, ",")))
			toggleBtn := actions.Objects[0].(*widget.Button)
			if r.Enabled {
				toggleBtn.SetText("Off")
			} else {
				toggleBtn.SetText("On")
			}
			idx := i
			toggleBtn.OnTapped = func() {
				_ = g.be.cfg.Update(func(f *config.File) {
					if idx < len(f.AlertRules) {
						f.AlertRules[idx].Enabled = !f.AlertRules[idx].Enabled
					}
				})
				g.rulesList.Refresh()
			}
			actions.Objects[1].(*widget.Button).OnTapped = func() {
				dialog.ShowConfirm("Удалить", "Удалить '"+r.Name+"'?", func(ok bool) {
					if !ok {
						return
					}
					_ = g.be.cfg.Update(func(f *config.File) {
						if idx < len(f.AlertRules) {
							f.AlertRules = append(f.AlertRules[:idx], f.AlertRules[idx+1:]...)
						}
					})
					g.rulesList.Refresh()
				}, g.win)
			}
		},
	)

	g.historyList = widget.NewList(
		func() int { return len(g.be.alerts.History(50)) },
		func() fyne.CanvasObject {
			l := widget.NewLabel("")
			l.TextStyle = fyne.TextStyle{Monospace: true}
			return l
		},
		func(i widget.ListItemID, obj fyne.CanvasObject) {
			hist := g.be.alerts.History(50)
			if i >= len(hist) {
				return
			}
			f := hist[i]
			obj.(*widget.Label).SetText(fmt.Sprintf(
				"[%s] %s · ×%d · %s → %s",
				f.At.Format("15:04:05"), f.RuleName, f.Count, f.Sample.Message, strings.Join(f.Delivered, ","),
			))
		},
	)

	splitV := container.NewVSplit(
		container.NewBorder(
			container.NewBorder(nil, nil, widget.NewLabelWithStyle("Правила", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}), addBtn, nil),
			nil, nil, nil, g.rulesList,
		),
		container.NewBorder(
			widget.NewLabelWithStyle("История срабатываний", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
			nil, nil, nil, g.historyList,
		),
	)
	splitV.SetOffset(0.5)

	bg := canvas.NewRectangle(colPanel)
	return container.NewStack(bg, container.NewPadded(splitV))
}


func (g *gui) openServerEditor(existing *models.Server) {
	s := models.Server{
		Name: "new-server", URL: "http://localhost:9000", Kind: "dev",
		Driver: models.DriverCfg{Type: "mock", Every: "5s"},
		Health: &models.HealthCfg{Type: "http", Path: "/healthz", Every: "10s"},
	}
	editIdx := -1
	if existing != nil {
		s = *existing
		snap := g.be.cfg.Snapshot().Servers
		for i := range snap {
			if snap[i].Name == existing.Name {
				editIdx = i
				break
			}
		}
	}

	name := widget.NewEntry()
	name.SetText(s.Name)
	url := widget.NewEntry()
	url.SetText(s.URL)
	kind := widget.NewSelect([]string{"production", "staging", "dev"}, nil)
	kind.SetSelected(valOr(s.Kind, "dev"))

	driverType := widget.NewSelect([]string{"mock", "http_poll", "tail_file", "none"}, nil)
	driverType.SetSelected(valOr(s.Driver.Type, "mock"))
	driverURL := widget.NewEntry()
	driverURL.SetText(s.Driver.URL)
	driverURL.SetPlaceHolder("для http_poll — полный URL JSON-endpoint'а")
	driverPath := widget.NewEntry()
	driverPath.SetText(s.Driver.Path)
	driverPath.SetPlaceHolder("для tail_file — путь к .jsonl")
	driverEvery := widget.NewEntry()
	driverEvery.SetText(valOr(s.Driver.Every, "5s"))

	if s.Health == nil {
		s.Health = &models.HealthCfg{Type: "http", Path: "/", Every: "10s"}
	}
	healthType := widget.NewSelect([]string{"http", "tcp"}, nil)
	healthType.SetSelected(valOr(s.Health.Type, "http"))
	healthPath := widget.NewEntry()
	healthPath.SetText(valOr(s.Health.Path, "/"))
	healthEvery := widget.NewEntry()
	healthEvery.SetText(valOr(s.Health.Every, "10s"))

	sshHost := widget.NewEntry()
	sshUser := widget.NewEntry()
	sshKey := widget.NewEntry()
	sshPort := widget.NewEntry()
	sshPort.SetText("22")
	if s.SSH != nil {
		sshHost.SetText(s.SSH.Host)
		sshUser.SetText(s.SSH.User)
		sshKey.SetText(s.SSH.KeyFile)
		if s.SSH.Port > 0 {
			sshPort.SetText(strconv.Itoa(s.SSH.Port))
		}
	}

	form := widget.NewForm(
		widget.NewFormItem("Name", name),
		widget.NewFormItem("URL", url),
		widget.NewFormItem("Kind", kind),
		widget.NewFormItem("Driver type", driverType),
		widget.NewFormItem("Driver URL", driverURL),
		widget.NewFormItem("Driver path", driverPath),
		widget.NewFormItem("Driver every", driverEvery),
		widget.NewFormItem("Health type", healthType),
		widget.NewFormItem("Health path", healthPath),
		widget.NewFormItem("Health every", healthEvery),
		widget.NewFormItem("SSH host", sshHost),
		widget.NewFormItem("SSH user", sshUser),
		widget.NewFormItem("SSH key file", sshKey),
		widget.NewFormItem("SSH port", sshPort),
	)

	dlg := dialog.NewCustomConfirm("Сервер: "+s.Name, "Сохранить", "Отмена",
		container.NewScroll(form), func(ok bool) {
			if !ok || name.Text == "" {
				return
			}
			newS := models.Server{
				Name: name.Text, URL: url.Text, Kind: kind.Selected,
				Driver: models.DriverCfg{Type: driverType.Selected, URL: driverURL.Text, Path: driverPath.Text, Every: driverEvery.Text},
				Health: &models.HealthCfg{Type: healthType.Selected, Path: healthPath.Text, Every: healthEvery.Text},
			}
			if sshHost.Text != "" {
				p, _ := strconv.Atoi(sshPort.Text)
				newS.SSH = &models.SSHConfig{Host: sshHost.Text, User: sshUser.Text, KeyFile: sshKey.Text, Port: p}
			}
			_ = g.be.cfg.Update(func(f *config.File) {
				if editIdx >= 0 && editIdx < len(f.Servers) {
					newS.Status = f.Servers[editIdx].Status
					f.Servers[editIdx] = newS
				} else {
					f.Servers = append(f.Servers, newS)
				}
			})
			g.be.dm.Sync(context.Background(), g.be.cfg.Snapshot().Servers)
			g.refreshAll()
		}, g.win)
	dlg.Resize(fyne.NewSize(620, 700))
	dlg.Show()
}

func (g *gui) openHeadersDialog() {
	lines := make([]string, 0, len(g.reqHeaders))
	for k, v := range g.reqHeaders {
		lines = append(lines, k+": "+v)
	}
	sort.Strings(lines)
	entry := widget.NewMultiLineEntry()
	entry.SetText(strings.Join(lines, "\n"))
	entry.SetPlaceHolder("Authorization: Bearer ...\nX-Custom: value")
	entry.Wrapping = fyne.TextWrapWord
	dlg := dialog.NewCustomConfirm("Headers (Key: Value на строку)", "Сохранить", "Отмена",
		container.NewScroll(entry), func(ok bool) {
			if !ok {
				return
			}
			h := map[string]string{}
			for _, line := range strings.Split(entry.Text, "\n") {
				idx := strings.Index(line, ":")
				if idx < 1 {
					continue
				}
				h[strings.TrimSpace(line[:idx])] = strings.TrimSpace(line[idx+1:])
			}
			g.reqHeaders = h
		}, g.win)
	dlg.Resize(fyne.NewSize(520, 360))
	dlg.Show()
}

func (g *gui) openSaveRequestDialog() {
	name := widget.NewEntry()
	name.SetPlaceHolder("Название")
	group := widget.NewEntry()
	group.SetPlaceHolder("Group (Health/Debug/...)")
	fav := widget.NewCheck("Избранное", nil)
	form := widget.NewForm(
		widget.NewFormItem("Имя", name),
		widget.NewFormItem("Группа", group),
		widget.NewFormItem("", fav),
	)
	dlg := dialog.NewCustomConfirm("Сохранить запрос", "Сохранить", "Отмена", form, func(ok bool) {
		if !ok || name.Text == "" {
			return
		}
		_ = g.be.cfg.Update(func(f *config.File) {
			f.SavedRequests = append(f.SavedRequests, models.SavedRequest{
				ID: "req_" + strconv.FormatInt(time.Now().UnixNano(), 36),
				Name: name.Text, Group: valOr(group.Text, "My"),
				Method: g.reqMethod.Selected, Path: g.reqPath.Text,
				Server: g.activeServer, Headers: g.reqHeaders, Favorite: fav.Checked,
			})
		})
		if g.savedList != nil {
			g.savedList.Refresh()
		}
	}, g.win)
	dlg.Resize(fyne.NewSize(440, 240))
	dlg.Show()
}

func (g *gui) openAlertRuleDialog() {
	name := widget.NewEntry()
	regex := widget.NewEntry()
	regex.SetPlaceHolder("regex (пусто = любой)")
	level := widget.NewSelect([]string{"", "info", "warn", "error"}, nil)
	level.SetSelected("error")
	threshold := widget.NewEntry()
	threshold.SetText("10")
	windowE := widget.NewEntry()
	windowE.SetText("1m")
	channels := widget.NewEntry()
	channels.SetPlaceHolder("slack-ops, telegram-oncall")
	form := widget.NewForm(
		widget.NewFormItem("Name", name),
		widget.NewFormItem("Regex", regex),
		widget.NewFormItem("Level ≥", level),
		widget.NewFormItem("Threshold", threshold),
		widget.NewFormItem("Window", windowE),
		widget.NewFormItem("Channels (CSV)", channels),
	)
	dlg := dialog.NewCustomConfirm("Новое alert-правило", "Создать", "Отмена", form, func(ok bool) {
		if !ok || name.Text == "" {
			return
		}
		th, _ := strconv.Atoi(threshold.Text)
		if th == 0 {
			th = 10
		}
		_ = g.be.cfg.Update(func(f *config.File) {
			f.AlertRules = append(f.AlertRules, models.AlertRule{
				ID: "alert_" + strconv.FormatInt(time.Now().UnixNano(), 36),
				Name: name.Text, Enabled: true,
				Regex: regex.Text, Level: level.Selected,
				Threshold: th, Window: windowE.Text, Cooldown: "5m",
				Channels: splitCSV(channels.Text),
			})
		})
		g.rulesList.Refresh()
	}, g.win)
	dlg.Resize(fyne.NewSize(460, 420))
	dlg.Show()
}

func (g *gui) openEventDetail(e models.Event) {
	sameFP := []models.Event{}
	g.evMu.RLock()
	for _, x := range g.events {
		if x.Fingerprint == e.Fingerprint && x.ID != e.ID {
			sameFP = append(sameFP, x)
			if len(sameFP) >= 10 {
				break
			}
		}
	}
	g.evMu.RUnlock()

	var b strings.Builder
	fmt.Fprintf(&b, "LEVEL:    %s\nTIME:     %s\nSERVER:   %s\nSERVICE:  %s\nFP:       %s\n\nMESSAGE:\n%s\n",
		e.Level, e.Timestamp.Format(time.RFC3339), e.Server, e.Service, e.Fingerprint, e.Message)
	if e.Trace != "" {
		fmt.Fprintf(&b, "\nTRACE:\n%s\n", e.Trace)
	}
	if len(e.Labels) > 0 {
		b.WriteString("\nLABELS:\n")
		for k, v := range e.Labels {
			fmt.Fprintf(&b, "  %s = %s\n", k, v)
		}
	}
	if len(sameFP) > 0 {
		fmt.Fprintf(&b, "\nSAME FINGERPRINT (×%d):\n", len(sameFP))
		for _, x := range sameFP {
			fmt.Fprintf(&b, "  [%s] %s · %s\n", x.Timestamp.Format("15:04:05"), x.Server, x.Message)
		}
	}

	entry := widget.NewMultiLineEntry()
	entry.SetText(b.String())
	entry.Wrapping = fyne.TextWrapWord
	entry.TextStyle = fyne.TextStyle{Monospace: true}
	entry.Disable()
	scroll := container.NewScroll(entry)
	scroll.SetMinSize(fyne.NewSize(780, 480))
	dlg := dialog.NewCustom("Event · "+e.Service+" · "+e.Server, "Закрыть", scroll, g.win)
	dlg.Resize(fyne.NewSize(900, 600))
	dlg.Show()
}

func (g *gui) openPalette() {
	type act struct {
		label string
		fn    func()
	}
	var actions []act
	for _, s := range g.be.cfg.Snapshot().Servers {
		srv := s.Name
		actions = append(actions, act{"→ server: " + s.Name, func() { g.activeServer = srv }})
	}
	for _, r := range g.be.cfg.Snapshot().SavedRequests {
		rr := r
		actions = append(actions, act{"→ saved: " + r.Name + " [" + r.Method + " " + r.Path + "]", func() {
			g.reqMethod.SetSelected(rr.Method)
			g.reqPath.SetText(rr.Path)
			if rr.Server != "" {
				g.activeServer = rr.Server
			}
		}})
	}
	actions = append(actions,
		act{"⚙ Settings", g.openSettings},
		act{"⏯ Пауза/Продолжить", g.togglePause},
		act{"⌫ Очистить Radar", g.clearRadar},
	)

	labels := make([]string, len(actions))
	for i, a := range actions {
		labels[i] = a.label
	}

	list := widget.NewList(
		func() int { return len(labels) },
		func() fyne.CanvasObject { return widget.NewLabel("") },
		func(i widget.ListItemID, obj fyne.CanvasObject) {
			obj.(*widget.Label).SetText(labels[i])
		},
	)
	var dlg dialog.Dialog
	list.OnSelected = func(id widget.ListItemID) {
		actions[id].fn()
		if dlg != nil {
			dlg.Hide()
		}
	}
	scroll := container.NewScroll(list)
	scroll.SetMinSize(fyne.NewSize(520, 380))
	dlg = dialog.NewCustom("Command palette", "Закрыть", scroll, g.win)
	dlg.Show()
}

func (g *gui) openSettings() {
	serversTab := g.buildSettingsServers()
	channelsTab := g.buildSettingsChannels()

	raw := widget.NewMultiLineEntry()
	raw.Wrapping = fyne.TextWrapOff
	raw.TextStyle = fyne.TextStyle{Monospace: true}
	raw.SetText(fmt.Sprintf("%+v", g.be.cfg.Snapshot()))
	raw.Disable()
	rawTab := container.NewScroll(raw)

	tabs := container.NewAppTabs(
		container.NewTabItem("Серверы", serversTab),
		container.NewTabItem("Alert channels", channelsTab),
		container.NewTabItem("Raw config", rawTab),
	)
	dlg := dialog.NewCustom("Settings · "+g.be.cfg.Path(), "Закрыть", tabs, g.win)
	dlg.Resize(fyne.NewSize(900, 680))
	dlg.Show()
}

func (g *gui) buildSettingsServers() fyne.CanvasObject {
	addBtn := widget.NewButtonWithIcon("+ Сервер", theme.ContentAddIcon(), func() {
		g.openServerEditor(nil)
	})
	addBtn.Importance = widget.SuccessImportance

	list := widget.NewList(
		func() int { return len(g.be.cfg.Snapshot().Servers) },
		func() fyne.CanvasObject {
			return container.NewBorder(nil, nil,
				widget.NewLabelWithStyle("●", fyne.TextAlignCenter, fyne.TextStyle{}),
				container.NewHBox(widget.NewButton("Edit", nil), widget.NewButton("×", nil)),
				widget.NewLabel(""),
			)
		},
		func(i widget.ListItemID, obj fyne.CanvasObject) {
			srvs := g.be.cfg.Snapshot().Servers
			if i >= len(srvs) {
				return
			}
			s := srvs[i]
			border := obj.(*fyne.Container)
			// NewBorder(nil,nil,dot,actions, name) → [name, dot, actions]
			name := border.Objects[0].(*widget.Label)
			actions := border.Objects[2].(*fyne.Container)
			name.SetText(s.Name + " · " + s.URL + " · " + valOr(s.Kind, "?"))
			idx := i
			actions.Objects[0].(*widget.Button).OnTapped = func() {
				cur := g.be.cfg.Snapshot().Servers[idx]
				g.openServerEditor(&cur)
			}
			actions.Objects[1].(*widget.Button).OnTapped = func() {
				cur := g.be.cfg.Snapshot().Servers[idx]
				dialog.ShowConfirm("Удалить", "Удалить '"+cur.Name+"'?", func(ok bool) {
					if !ok {
						return
					}
					_ = g.be.cfg.Update(func(f *config.File) {
						if idx < len(f.Servers) {
							f.Servers = append(f.Servers[:idx], f.Servers[idx+1:]...)
						}
					})
					g.be.dm.Sync(context.Background(), g.be.cfg.Snapshot().Servers)
					g.refreshAll()
				}, g.win)
			}
		},
	)

	return container.NewBorder(addBtn, nil, nil, nil, list)
}

func (g *gui) buildSettingsChannels() fyne.CanvasObject {
	addBtn := widget.NewButtonWithIcon("+ Канал", theme.ContentAddIcon(), func() { g.openChannelEditor(nil) })

	list := widget.NewList(
		func() int { return len(g.be.cfg.Snapshot().AlertChannels) },
		func() fyne.CanvasObject {
			return container.NewBorder(nil, nil,
				widget.NewLabel("kind"),
				container.NewHBox(widget.NewButton("Edit", nil), widget.NewButton("×", nil)),
				widget.NewLabel(""),
			)
		},
		func(i widget.ListItemID, obj fyne.CanvasObject) {
			chs := g.be.cfg.Snapshot().AlertChannels
			if i >= len(chs) {
				return
			}
			c := chs[i]
			border := obj.(*fyne.Container)
			// NewBorder(nil,nil,kind,actions, name) → [name, kind, actions]
			border.Objects[0].(*widget.Label).SetText(c.Name + " · " + c.URL)
			border.Objects[1].(*widget.Label).SetText(c.Type)
			idx := i
			actions := border.Objects[2].(*fyne.Container)
			actions.Objects[0].(*widget.Button).OnTapped = func() {
				cur := g.be.cfg.Snapshot().AlertChannels[idx]
				g.openChannelEditor(&cur)
			}
			actions.Objects[1].(*widget.Button).OnTapped = func() {
				cur := g.be.cfg.Snapshot().AlertChannels[idx]
				dialog.ShowConfirm("Удалить", "Удалить '"+cur.Name+"'?", func(ok bool) {
					if !ok {
						return
					}
					_ = g.be.cfg.Update(func(f *config.File) {
						if idx < len(f.AlertChannels) {
							f.AlertChannels = append(f.AlertChannels[:idx], f.AlertChannels[idx+1:]...)
						}
					})
				}, g.win)
			}
		},
	)

	return container.NewBorder(addBtn, nil, nil, nil, list)
}

func (g *gui) openChannelEditor(existing *models.AlertChannel) {
	c := models.AlertChannel{Type: "slack"}
	editIdx := -1
	if existing != nil {
		c = *existing
		snap := g.be.cfg.Snapshot().AlertChannels
		for i := range snap {
			if snap[i].Name == existing.Name {
				editIdx = i
				break
			}
		}
	}
	name := widget.NewEntry()
	name.SetText(c.Name)
	kind := widget.NewSelect([]string{"slack", "telegram", "webhook"}, nil)
	kind.SetSelected(valOr(c.Type, "slack"))
	url := widget.NewEntry()
	url.SetText(c.URL)
	token := widget.NewPasswordEntry()
	token.SetText(c.Token)
	chatID := widget.NewEntry()
	chatID.SetText(c.ChatID)
	form := widget.NewForm(
		widget.NewFormItem("Name", name),
		widget.NewFormItem("Type", kind),
		widget.NewFormItem("URL", url),
		widget.NewFormItem("Token", token),
		widget.NewFormItem("Chat ID", chatID),
	)
	dlg := dialog.NewCustomConfirm("Alert channel", "Сохранить", "Отмена", form, func(ok bool) {
		if !ok || name.Text == "" {
			return
		}
		newC := models.AlertChannel{Name: name.Text, Type: kind.Selected, URL: url.Text, Token: token.Text, ChatID: chatID.Text}
		_ = g.be.cfg.Update(func(f *config.File) {
			if editIdx >= 0 && editIdx < len(f.AlertChannels) {
				f.AlertChannels[editIdx] = newC
			} else {
				f.AlertChannels = append(f.AlertChannels, newC)
			}
		})
	}, g.win)
	dlg.Resize(fyne.NewSize(520, 400))
	dlg.Show()
}


func (g *gui) sendDebug() {
	if g.activeServer == "" {
		dialog.ShowInformation("Сервер не выбран", "Выбери сервер в сайдбаре слева", g.win)
		return
	}
	method := g.reqMethod.Selected
	path := strings.TrimSpace(g.reqPath.Text)
	if path == "" {
		return
	}
	g.respBox.Show()
	g.respLabel.SetText("… отправка")
	g.respBody.SetText("")

	go func() {
		snap := g.be.cfg.Snapshot()
		var srvURL string
		var auth *models.Auth
		for _, s := range snap.Servers {
			if s.Name == g.activeServer {
				srvURL = s.URL
				auth = s.Auth
				break
			}
		}
		target := path
		if !strings.HasPrefix(target, "http://") && !strings.HasPrefix(target, "https://") {
			target = strings.TrimRight(srvURL, "/") + "/" + strings.TrimLeft(path, "/")
		}
		start := time.Now()
		status, body, err := doDebugHTTP(context.Background(), method, target, g.reqHeaders, auth)
		dur := time.Since(start).Milliseconds()
		fyne.Do(func() {
			if err != nil {
				g.respLabel.SetText(fmt.Sprintf("ERR · %dms", dur))
				g.respBody.SetText(err.Error())
				return
			}
			g.respLabel.SetText(fmt.Sprintf("HTTP %d · %dms · %d bytes", status, dur, len(body)))
			if len(body) > 4000 {
				body = body[:4000] + "\n… (truncated)"
			}
			g.respBody.SetText(body)
		})
	}()
}

func (g *gui) runSSH(cmd string) {
	srv := g.runnerSrv.Selected
	if srv == "" {
		g.runnerOutput.SetText("выбери сервер с заполненным SSH config")
		return
	}
	if cmd == "" {
		return
	}
	g.runnerOutput.SetText("$ " + cmd + "\n…выполняется…")
	go func() {
		snap := g.be.cfg.Snapshot()
		var sshCfg *models.SSHConfig
		for _, s := range snap.Servers {
			if s.Name == srv && s.SSH != nil {
				sshCfg = s.SSH
				break
			}
		}
		if sshCfg == nil {
			fyne.Do(func() { g.runnerOutput.SetText("нет SSH config для " + srv) })
			return
		}
		res, err := runner.Exec(context.Background(), sshCfg, cmd, 15*time.Second)
		fyne.Do(func() {
			if err != nil {
				g.runnerOutput.SetText("$ " + cmd + "\nERROR: " + err.Error())
				return
			}
			out := fmt.Sprintf("$ %s\n# exit=%d · %dms\n", cmd, res.ExitCode, res.Duration.Milliseconds())
			out += res.Stdout
			if res.Stderr != "" {
				out += "\n[stderr]\n" + res.Stderr
			}
			g.runnerOutput.SetText(out)
		})
	}()
}

func (g *gui) togglePause() {
	g.paused = !g.paused
}

func (g *gui) clearRadar() {
	g.evMu.Lock()
	g.events = nil
	g.evMu.Unlock()
	if g.radarTable != nil {
		g.radarTable.Refresh()
	}
}


func (g *gui) consumeEvents() {
	ch := g.be.hub.Subscribe()
	defer g.be.hub.Unsubscribe(ch)
	for e := range ch {
		if g.paused {
			continue
		}
		g.rateStamps = append(g.rateStamps, time.Now())
		g.total++
		if !g.matchesFilter(e) {
			continue
		}
		g.evMu.Lock()
		g.events = append([]models.Event{e}, g.events...)
		if len(g.events) > 300 {
			g.events = g.events[:300]
		}
		g.evMu.Unlock()
		fyne.Do(func() { g.radarTable.Refresh() })
	}
}

func (g *gui) matchesFilter(e models.Event) bool {
	txt, _ := g.filterBox.Get()
	lvl, _ := g.levelBox.Get()
	srv, _ := g.serverBox.Get()
	if srv != "" && e.Server != srv {
		return false
	}
	if lvl != "" && levelOrder[e.Level] < levelOrder[lvl] {
		return false
	}
	if txt != "" {
		hay := strings.ToLower(e.Service + " " + e.Message + " " + e.Server + " " + e.Level)
		if !strings.Contains(hay, strings.ToLower(txt)) {
			return false
		}
	}
	return true
}

func (g *gui) refreshRadar() {
	recent := g.be.store.Recent(300)
	filtered := make([]models.Event, 0, len(recent))
	for i := len(recent) - 1; i >= 0; i-- {
		if g.matchesFilter(recent[i]) {
			filtered = append(filtered, recent[i])
		}
	}
	g.evMu.Lock()
	g.events = filtered
	g.evMu.Unlock()
	if g.radarTable != nil {
		fyne.Do(func() { g.radarTable.Refresh() })
	}
}

func (g *gui) refreshAll() {
	g.refreshRadar()
	g.updateCounters()
	fyne.Do(func() {
		g.refreshSidebarEmpty()
		if g.serverList != nil {
			g.serverList.Refresh()
		}
		if g.radarServerSelect != nil {
			g.radarServerSelect.Options = g.serverNamesWithEmpty()
			g.radarServerSelect.Refresh()
		}
		if g.runnerSrv != nil {
			g.runnerSrv.Options = g.sshServerNames()
			g.runnerSrv.Refresh()
		}
		if g.rulesList != nil {
			g.rulesList.Refresh()
		}
		if g.savedList != nil {
			g.savedList.Refresh()
		}
		if g.connBadge != nil {
			g.connBadge.Text = "live · in-process"
			g.connBadge.Color = colOK
			g.connBadge.Refresh()
		}
	})
}

func (g *gui) refreshStats() {
	events := g.be.store.Recent(2000)
	perLevel, perServer, perService := map[string]int{}, map[string]int{}, map[string]int{}
	fp := map[string]int{}
	fpSample := map[string]models.Event{}
	for _, e := range events {
		perLevel[e.Level]++
		perServer[e.Server]++
		perService[e.Service]++
		fp[e.Fingerprint]++
		fpSample[e.Fingerprint] = e
	}
	var b strings.Builder
	fmt.Fprintf(&b, "TOTAL: %d events in ring buffer\n\n", len(events))
	b.WriteString("BY LEVEL:\n")
	for _, k := range sortedKeys(perLevel) {
		fmt.Fprintf(&b, "  %-10s %d\n", k, perLevel[k])
	}
	b.WriteString("\nBY SERVER:\n")
	for _, k := range sortedKeys(perServer) {
		fmt.Fprintf(&b, "  %-24s %d\n", k, perServer[k])
	}
	b.WriteString("\nBY SERVICE:\n")
	for _, k := range sortedKeys(perService) {
		fmt.Fprintf(&b, "  %-24s %d\n", k, perService[k])
	}
	b.WriteString("\nTOP GROUPS (by fingerprint):\n")
	type pair struct {
		msg string
		n   int
	}
	pairs := make([]pair, 0, len(fp))
	for k, n := range fp {
		pairs = append(pairs, pair{fpSample[k].Service + " · " + fpSample[k].Message, n})
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].n > pairs[j].n })
	for i, p := range pairs {
		if i >= 20 {
			break
		}
		msg := p.msg
		if len(msg) > 100 {
			msg = msg[:100] + "…"
		}
		fmt.Fprintf(&b, "  ×%-4d %s\n", p.n, msg)
	}
	fyne.Do(func() { g.statsText.SetText(b.String()) })
}

func (g *gui) updateCounters() {
	srvs := g.be.cfg.Snapshot().Servers
	online := 0
	for _, s := range srvs {
		if s.Status.State == "online" {
			online++
		}
	}
	cutoff := time.Now().Add(-time.Minute)
	keep := g.rateStamps[:0]
	for _, t := range g.rateStamps {
		if t.After(cutoff) {
			keep = append(keep, t)
		}
	}
	g.rateStamps = keep
	fyne.Do(func() {
		if g.serverCount != nil {
			g.serverCount.SetText(fmt.Sprintf("%d/%d", online, len(srvs)))
		}
		if g.metricTotal != nil {
			g.metricTotal.SetText(strconv.Itoa(g.total))
		}
		if g.metricRate != nil {
			g.metricRate.SetText(strconv.Itoa(len(keep)))
		}
	})
}

func (g *gui) startTickers() {
	t := time.NewTicker(1 * time.Second)
	defer t.Stop()
	for range t.C {
		g.updateCounters()
		fyne.Do(func() {
			if g.serverList != nil {
				g.serverList.Refresh()
			}
			if g.historyList != nil {
				g.historyList.Refresh()
			}
		})
	}
}


func (g *gui) serverNamesWithEmpty() []string {
	out := []string{""}
	for _, s := range g.be.cfg.Snapshot().Servers {
		out = append(out, s.Name)
	}
	return out
}

func (g *gui) sshServerNames() []string {
	var out []string
	for _, s := range g.be.cfg.Snapshot().Servers {
		if s.SSH != nil && s.SSH.Host != "" {
			out = append(out, s.Name)
		}
	}
	return out
}

func valOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func splitCSV(s string) []string {
	out := []string{}
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func sortedKeys(m map[string]int) []string {
	k := make([]string, 0, len(m))
	for kk := range m {
		k = append(k, kk)
	}
	sort.Strings(k)
	return k
}

func smallCap(s string) *canvas.Text {
	t := canvas.NewText(s, colMuted)
	t.TextSize = 10
	t.TextStyle = fyne.TextStyle{Bold: true}
	return t
}


type fixedLayout struct{ w, h float32 }

func (f *fixedLayout) MinSize(objs []fyne.CanvasObject) fyne.Size { return fyne.NewSize(f.w, f.h) }
func (f *fixedLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	for _, o := range objs {
		o.Resize(size)
	}
}
