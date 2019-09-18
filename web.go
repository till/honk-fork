//
// Copyright (c) 2019 Ted Unangst <tedu@tedunangst.com>
//
// Permission to use, copy, modify, and distribute this software for any
// purpose with or without fee is hereby granted, provided that the above
// copyright notice and this permission notice appear in all copies.
//
// THE SOFTWARE IS PROVIDED "AS IS" AND THE AUTHOR DISCLAIMS ALL WARRANTIES
// WITH REGARD TO THIS SOFTWARE INCLUDING ALL IMPLIED WARRANTIES OF
// MERCHANTABILITY AND FITNESS. IN NO EVENT SHALL THE AUTHOR BE LIABLE FOR
// ANY SPECIAL, DIRECT, INDIRECT, OR CONSEQUENTIAL DAMAGES OR ANY DAMAGES
// WHATSOEVER RESULTING FROM LOSS OF USE, DATA OR PROFITS, WHETHER IN AN
// ACTION OF CONTRACT, NEGLIGENCE OR OTHER TORTIOUS ACTION, ARISING OUT OF
// OR IN CONNECTION WITH THE USE OR PERFORMANCE OF THIS SOFTWARE.

package main

import (
	"bytes"
	"fmt"
	"html"
	"html/template"
	"io"
	"io/ioutil"
	"log"
	notrand "math/rand"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"humungus.tedunangst.com/r/webs/css"
	"humungus.tedunangst.com/r/webs/htfilter"
	"humungus.tedunangst.com/r/webs/httpsig"
	"humungus.tedunangst.com/r/webs/image"
	"humungus.tedunangst.com/r/webs/junk"
	"humungus.tedunangst.com/r/webs/login"
	"humungus.tedunangst.com/r/webs/rss"
	"humungus.tedunangst.com/r/webs/templates"
)

var readviews *templates.Template

var userSep = "u"
var honkSep = "h"

var donotfedafterdark = make(map[string]bool)

func stealthed(r *http.Request) bool {
	addr := r.Header.Get("X-Forwarded-For")
	fake := donotfedafterdark[addr]
	if fake {
		log.Printf("faking 404 for %s", addr)
	}
	return fake
}

func getuserstyle(u *login.UserInfo) template.CSS {
	if u == nil {
		return ""
	}
	user, _ := butwhatabout(u.Username)
	if user.SkinnyCSS {
		return "main { max-width: 700px; }"
	}
	return ""
}

func getInfo(r *http.Request) map[string]interface{} {
	u := login.GetUserInfo(r)
	templinfo := make(map[string]interface{})
	templinfo["StyleParam"] = getstyleparam("views/style.css")
	templinfo["LocalStyleParam"] = getstyleparam("views/local.css")
	templinfo["UserStyle"] = getuserstyle(u)
	templinfo["ServerName"] = serverName
	templinfo["IconName"] = iconName
	templinfo["UserInfo"] = u
	templinfo["UserSep"] = userSep
	return templinfo
}

func homepage(w http.ResponseWriter, r *http.Request) {
	templinfo := getInfo(r)
	u := login.GetUserInfo(r)
	var honks []*Honk
	var userid int64 = -1
	if r.URL.Path == "/front" || u == nil {
		honks = getpublichonks()
	} else {
		userid = u.UserID
		if r.URL.Path == "/atme" {
			templinfo["PageName"] = "atme"
			honks = gethonksforme(userid)
		} else {
			templinfo["PageName"] = "home"
			honks = gethonksforuser(userid)
			honks = osmosis(honks, userid)
		}
		if len(honks) > 0 {
			templinfo["TopXID"] = honks[0].XID
		}
		templinfo["HonkCSRF"] = login.GetCSRF("honkhonk", r)
	}

	templinfo["ShowRSS"] = true
	templinfo["ServerMessage"] = serverMsg
	honkpage(w, u, honks, templinfo)
}

func showfunzone(w http.ResponseWriter, r *http.Request) {
	var emunames, memenames []string
	dir, err := os.Open("emus")
	if err == nil {
		emunames, _ = dir.Readdirnames(0)
		dir.Close()
	}
	for i, e := range emunames {
		if len(e) > 4 {
			emunames[i] = e[:len(e)-4]
		}
	}
	dir, err = os.Open("memes")
	if err == nil {
		memenames, _ = dir.Readdirnames(0)
		dir.Close()
	}
	templinfo := getInfo(r)
	templinfo["Emus"] = emunames
	templinfo["Memes"] = memenames
	err = readviews.Execute(w, "funzone.html", templinfo)
	if err != nil {
		log.Print(err)
	}
}

func showrss(w http.ResponseWriter, r *http.Request) {
	name := mux.Vars(r)["name"]

	var honks []*Honk
	if name != "" {
		honks = gethonksbyuser(name, false)
	} else {
		honks = getpublichonks()
	}
	if len(honks) > 20 {
		honks = honks[0:20]
	}
	reverbolate(-1, honks)

	home := fmt.Sprintf("https://%s/", serverName)
	base := home
	if name != "" {
		home += "u/" + name
		name += " "
	}
	feed := rss.Feed{
		Title:       name + "honk",
		Link:        home,
		Description: name + "honk rss",
		Image: &rss.Image{
			URL:   base + "icon.png",
			Title: name + "honk rss",
			Link:  home,
		},
	}
	var modtime time.Time
	for _, honk := range honks {
		if !firstclass(honk) {
			continue
		}
		desc := string(honk.HTML)
		for _, d := range honk.Donks {
			desc += fmt.Sprintf(`<p><a href="%s">Attachment: %s</a>`,
				d.URL, html.EscapeString(d.Name))
		}

		feed.Items = append(feed.Items, &rss.Item{
			Title:       fmt.Sprintf("%s %s %s", honk.Username, honk.What, honk.XID),
			Description: rss.CData{desc},
			Link:        honk.URL,
			PubDate:     honk.Date.Format(time.RFC1123),
			Guid:        &rss.Guid{IsPermaLink: true, Value: honk.URL},
		})
		if honk.Date.After(modtime) {
			modtime = honk.Date
		}
	}
	w.Header().Set("Cache-Control", "max-age=300")
	w.Header().Set("Last-Modified", modtime.Format(http.TimeFormat))

	err := feed.Write(w)
	if err != nil {
		log.Printf("error writing rss: %s", err)
	}
}

func crappola(j junk.Junk) bool {
	t, _ := j.GetString("type")
	a, _ := j.GetString("actor")
	o, _ := j.GetString("object")
	if t == "Delete" && a == o {
		log.Printf("crappola from %s", a)
		return true
	}
	return false
}

func ping(user *WhatAbout, who string) {
	box, err := getboxes(who)
	if err != nil {
		log.Printf("no inbox for ping: %s", err)
		return
	}
	j := junk.New()
	j["@context"] = itiswhatitis
	j["type"] = "Ping"
	j["id"] = user.URL + "/ping/" + xfiltrate()
	j["actor"] = user.URL
	j["to"] = who
	keyname, key := ziggy(user.Name)
	err = PostJunk(keyname, key, box.In, j)
	if err != nil {
		log.Printf("can't send ping: %s", err)
		return
	}
	log.Printf("sent ping to %s: %s", who, j["id"])
}

func pong(user *WhatAbout, who string, obj string) {
	box, err := getboxes(who)
	if err != nil {
		log.Printf("no inbox for pong %s : %s", who, err)
		return
	}
	j := junk.New()
	j["@context"] = itiswhatitis
	j["type"] = "Pong"
	j["id"] = user.URL + "/pong/" + xfiltrate()
	j["actor"] = user.URL
	j["to"] = who
	j["object"] = obj
	keyname, key := ziggy(user.Name)
	err = PostJunk(keyname, key, box.In, j)
	if err != nil {
		log.Printf("can't send pong: %s", err)
		return
	}
}

func inbox(w http.ResponseWriter, r *http.Request) {
	name := mux.Vars(r)["name"]
	user, err := butwhatabout(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	var buf bytes.Buffer
	io.Copy(&buf, r.Body)
	payload := buf.Bytes()
	j, err := junk.Read(bytes.NewReader(payload))
	if err != nil {
		log.Printf("bad payload: %s", err)
		io.WriteString(os.Stdout, "bad payload\n")
		os.Stdout.Write(payload)
		io.WriteString(os.Stdout, "\n")
		return
	}
	if crappola(j) {
		return
	}
	keyname, err := httpsig.VerifyRequest(r, payload, zaggy)
	if err != nil {
		log.Printf("inbox message failed signature: %s", err)
		if keyname != "" {
			keyname, err = makeitworksomehowwithoutregardforkeycontinuity(keyname, r, payload)
			if err != nil {
				log.Printf("still failed: %s", err)
			}
		}
		if err != nil {
			return
		}
	}
	what, _ := j.GetString("type")
	if what == "Like" {
		return
	}
	who, _ := j.GetString("actor")
	origin := keymatch(keyname, who)
	if origin == "" {
		log.Printf("keyname actor mismatch: %s <> %s", keyname, who)
		return
	}
	objid, _ := j.GetString("id")
	if thoudostbitethythumb(user.ID, []string{who}, objid) {
		log.Printf("ignoring thumb sucker %s", who)
		return
	}
	switch what {
	case "Ping":
		obj, _ := j.GetString("id")
		log.Printf("ping from %s: %s", who, obj)
		pong(user, who, obj)
	case "Pong":
		obj, _ := j.GetString("object")
		log.Printf("pong from %s: %s", who, obj)
	case "Follow":
		obj, _ := j.GetString("object")
		if obj == user.URL {
			log.Printf("updating honker follow: %s", who)
			stmtSaveDub.Exec(user.ID, who, who, "dub")
			go rubadubdub(user, j)
		} else {
			log.Printf("can't follow %s", obj)
		}
	case "Accept":
		log.Printf("updating honker accept: %s", who)
		_, err = stmtUpdateFlavor.Exec("sub", user.ID, who, "presub")
		if err != nil {
			log.Printf("error updating honker: %s", err)
			return
		}
	case "Update":
		obj, ok := j.GetMap("object")
		if ok {
			what, _ := obj.GetString("type")
			switch what {
			case "Person":
				return
			case "Question":
				return
			case "Note":
				go consumeactivity(user, j, origin)
				return
			}
		}
		log.Printf("unknown Update activity")
		fd, _ := os.OpenFile("savedinbox.json", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
		j.Write(fd)
		io.WriteString(fd, "\n")
		fd.Close()

	case "Undo":
		obj, ok := j.GetMap("object")
		if !ok {
			log.Printf("unknown undo no object")
		} else {
			what, _ := obj.GetString("type")
			switch what {
			case "Follow":
				log.Printf("updating honker undo: %s", who)
				_, err = stmtUpdateFlavor.Exec("undub", user.ID, who, "dub")
				if err != nil {
					log.Printf("error updating honker: %s", err)
					return
				}
			case "Announce":
				xid, _ := obj.GetString("object")
				log.Printf("undo announce: %s", xid)
			case "Like":
			default:
				log.Printf("unknown undo: %s", what)
			}
		}
	default:
		go consumeactivity(user, j, origin)
	}
}

func ximport(w http.ResponseWriter, r *http.Request) {
	xid := r.FormValue("xid")
	p, _ := investigate(xid)
	if p != nil {
		xid = p.XID
	}
	j, err := GetJunk(xid)
	if err != nil {
		http.Error(w, "error getting external object", http.StatusInternalServerError)
		log.Printf("error getting external object: %s", err)
		return
	}
	log.Printf("importing %s", xid)
	u := login.GetUserInfo(r)
	user, _ := butwhatabout(u.Username)

	what, _ := j.GetString("type")
	if isactor(what) {
		outbox, _ := j.GetString("outbox")
		gimmexonks(user, outbox)
		http.Redirect(w, r, "/h?xid="+url.QueryEscape(xid), http.StatusSeeOther)
		return
	}
	xonk := xonkxonk(user, j, originate(xid))
	convoy := ""
	if xonk != nil {
		convoy = xonk.Convoy
		savexonk(xonk)
	}
	http.Redirect(w, r, "/t?c="+url.QueryEscape(convoy), http.StatusSeeOther)
}

func xzone(w http.ResponseWriter, r *http.Request) {
	u := login.GetUserInfo(r)
	rows, err := stmtRecentHonkers.Query(u.UserID, u.UserID)
	if err != nil {
		log.Printf("query err: %s", err)
		return
	}
	defer rows.Close()
	var honkers []Honker
	for rows.Next() {
		var xid string
		rows.Scan(&xid)
		honkers = append(honkers, Honker{XID: xid})
	}
	rows.Close()
	for i, _ := range honkers {
		_, honkers[i].Handle = handles(honkers[i].XID)
	}
	templinfo := getInfo(r)
	templinfo["XCSRF"] = login.GetCSRF("ximport", r)
	templinfo["Honkers"] = honkers
	err = readviews.Execute(w, "xzone.html", templinfo)
	if err != nil {
		log.Print(err)
	}
}

func outbox(w http.ResponseWriter, r *http.Request) {
	name := mux.Vars(r)["name"]
	user, err := butwhatabout(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if stealthed(r) {
		http.NotFound(w, r)
		return
	}

	honks := gethonksbyuser(name, false)
	if len(honks) > 20 {
		honks = honks[0:20]
	}

	var jonks []junk.Junk
	for _, h := range honks {
		j, _ := jonkjonk(user, h)
		jonks = append(jonks, j)
	}

	j := junk.New()
	j["@context"] = itiswhatitis
	j["id"] = user.URL + "/outbox"
	j["type"] = "OrderedCollection"
	j["totalItems"] = len(jonks)
	j["orderedItems"] = jonks

	w.Header().Set("Content-Type", theonetruename)
	j.Write(w)
}

func emptiness(w http.ResponseWriter, r *http.Request) {
	name := mux.Vars(r)["name"]
	user, err := butwhatabout(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	colname := "/followers"
	if strings.HasSuffix(r.URL.Path, "/following") {
		colname = "/following"
	}
	j := junk.New()
	j["@context"] = itiswhatitis
	j["id"] = user.URL + colname
	j["type"] = "OrderedCollection"
	j["totalItems"] = 0
	j["orderedItems"] = []junk.Junk{}

	w.Header().Set("Content-Type", theonetruename)
	j.Write(w)
}

func showuser(w http.ResponseWriter, r *http.Request) {
	name := mux.Vars(r)["name"]
	user, err := butwhatabout(name)
	if err != nil {
		log.Printf("user not found %s: %s", name, err)
		http.NotFound(w, r)
		return
	}
	if friendorfoe(r.Header.Get("Accept")) {
		j := asjonker(user)
		w.Header().Set("Content-Type", theonetruename)
		j.Write(w)
		return
	}
	u := login.GetUserInfo(r)
	honks := gethonksbyuser(name, u != nil && u.Username == name)
	templinfo := getInfo(r)
	filt := htfilter.New()
	templinfo["Name"] = user.Name
	whatabout := user.About
	whatabout = obfusbreak(user.About)
	templinfo["WhatAbout"], _ = filt.String(whatabout)
	templinfo["ServerMessage"] = ""
	templinfo["HonkCSRF"] = login.GetCSRF("honkhonk", r)
	honkpage(w, u, honks, templinfo)
}

func showhonker(w http.ResponseWriter, r *http.Request) {
	u := login.GetUserInfo(r)
	name := mux.Vars(r)["name"]
	var honks []*Honk
	if name == "" {
		name = r.FormValue("xid")
		honks = gethonksbyxonker(u.UserID, name)
	} else {
		honks = gethonksbyhonker(u.UserID, name)
	}
	name = html.EscapeString(name)
	msg := fmt.Sprintf(`honks by honker: <a href="%s" ref="noreferrer">%s</a>`, name, name)
	templinfo := getInfo(r)
	templinfo["PageName"] = "honker"
	templinfo["ServerMessage"] = template.HTML(msg)
	templinfo["HonkCSRF"] = login.GetCSRF("honkhonk", r)
	honkpage(w, u, honks, templinfo)
}

func showcombo(w http.ResponseWriter, r *http.Request) {
	name := mux.Vars(r)["name"]
	u := login.GetUserInfo(r)
	honks := gethonksbycombo(u.UserID, name)
	honks = osmosis(honks, u.UserID)
	templinfo := getInfo(r)
	templinfo["PageName"] = "combo"
	templinfo["ServerMessage"] = "honks by combo: " + name
	templinfo["HonkCSRF"] = login.GetCSRF("honkhonk", r)
	honkpage(w, u, honks, templinfo)
}
func showconvoy(w http.ResponseWriter, r *http.Request) {
	c := r.FormValue("c")
	u := login.GetUserInfo(r)
	honks := gethonksbyconvoy(u.UserID, c)
	templinfo := getInfo(r)
	templinfo["ServerMessage"] = "honks in convoy: " + c
	templinfo["HonkCSRF"] = login.GetCSRF("honkhonk", r)
	honkpage(w, u, honks, templinfo)
}
func showsearch(w http.ResponseWriter, r *http.Request) {
	q := r.FormValue("q")
	u := login.GetUserInfo(r)
	honks := gethonksbysearch(u.UserID, q)
	templinfo := getInfo(r)
	templinfo["ServerMessage"] = "honks for search: " + q
	templinfo["HonkCSRF"] = login.GetCSRF("honkhonk", r)
	honkpage(w, u, honks, templinfo)
}
func showontology(w http.ResponseWriter, r *http.Request) {
	name := mux.Vars(r)["name"]
	u := login.GetUserInfo(r)
	var userid int64 = -1
	if u != nil {
		userid = u.UserID
	}
	honks := gethonksbyontology(userid, "#"+name)
	templinfo := getInfo(r)
	templinfo["ServerMessage"] = "honks by ontology: " + name
	templinfo["HonkCSRF"] = login.GetCSRF("honkhonk", r)
	honkpage(w, u, honks, templinfo)
}

func thelistingoftheontologies(w http.ResponseWriter, r *http.Request) {
	u := login.GetUserInfo(r)
	var userid int64 = -1
	if u != nil {
		userid = u.UserID
	}
	rows, err := stmtSelectOnts.Query(userid)
	if err != nil {
		log.Printf("selection error: %s", err)
		return
	}
	defer rows.Close()
	var onts [][]string
	for rows.Next() {
		var o string
		err := rows.Scan(&o)
		if err != nil {
			log.Printf("error scanning ont: %s", err)
			continue
		}
		onts = append(onts, []string{o, o[1:]})
	}
	if u == nil {
		w.Header().Set("Cache-Control", "max-age=300")
	}
	templinfo := getInfo(r)
	templinfo["Onts"] = onts
	err = readviews.Execute(w, "onts.html", templinfo)
	if err != nil {
		log.Print(err)
	}
}

func showhonk(w http.ResponseWriter, r *http.Request) {
	name := mux.Vars(r)["name"]
	user, err := butwhatabout(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if stealthed(r) {
		http.NotFound(w, r)
		return
	}

	xid := fmt.Sprintf("https://%s%s", serverName, r.URL.Path)
	honk := getxonk(user.ID, xid)
	if honk == nil {
		http.NotFound(w, r)
		return
	}
	u := login.GetUserInfo(r)
	if u != nil && u.UserID != user.ID {
		u = nil
	}
	if !honk.Public {
		if u == nil {
			http.NotFound(w, r)
			return

		}
		templinfo := getInfo(r)
		templinfo["ServerMessage"] = "one honk maybe more"
		templinfo["HonkCSRF"] = login.GetCSRF("honkhonk", r)
		honkpage(w, u, []*Honk{honk}, templinfo)
		return
	}
	rawhonks := gethonksbyconvoy(honk.UserID, honk.Convoy)
	if friendorfoe(r.Header.Get("Accept")) {
		for _, h := range rawhonks {
			if h.RID == honk.XID && h.Public && (h.Whofore == 2 || h.IsAcked()) {
				honk.Replies = append(honk.Replies, h)
			}
		}
		donksforhonks([]*Honk{honk})
		_, j := jonkjonk(user, honk)
		j["@context"] = itiswhatitis
		w.Header().Set("Content-Type", theonetruename)
		j.Write(w)
		return
	}
	var honks []*Honk
	for _, h := range rawhonks {
		if h.Public && (h.Whofore == 2 || h.IsAcked()) {
			honks = append(honks, h)
		}
	}

	templinfo := getInfo(r)
	templinfo["ServerMessage"] = "one honk maybe more"
	templinfo["HonkCSRF"] = login.GetCSRF("honkhonk", r)
	honkpage(w, u, honks, templinfo)
}

func honkpage(w http.ResponseWriter, u *login.UserInfo, honks []*Honk, templinfo map[string]interface{}) {
	var userid int64 = -1
	if u != nil {
		userid = u.UserID
	}
	if u == nil {
		w.Header().Set("Cache-Control", "max-age=60")
	}
	reverbolate(userid, honks)
	templinfo["Honks"] = honks
	err := readviews.Execute(w, "honkpage.html", templinfo)
	if err != nil {
		log.Print(err)
	}
}

func saveuser(w http.ResponseWriter, r *http.Request) {
	whatabout := r.FormValue("whatabout")
	u := login.GetUserInfo(r)
	db := opendatabase()
	options := ""
	if r.FormValue("skinny") == "skinny" {
		options += " skinny "
	}
	_, err := db.Exec("update users set about = ?, options = ? where username = ?", whatabout, options, u.Username)
	if err != nil {
		log.Printf("error bouting what: %s", err)
	}

	http.Redirect(w, r, "/account", http.StatusSeeOther)
}

func submitbonk(w http.ResponseWriter, r *http.Request) {
	xid := r.FormValue("xid")
	userinfo := login.GetUserInfo(r)
	user, _ := butwhatabout(userinfo.Username)

	log.Printf("bonking %s", xid)

	xonk := getxonk(userinfo.UserID, xid)
	if xonk == nil {
		return
	}
	if !xonk.Public {
		return
	}
	donksforhonks([]*Honk{xonk})

	_, err := stmtUpdateFlags.Exec(flagIsBonked, xonk.ID)
	if err != nil {
		log.Printf("error acking bonk: %s", err)
	}

	oonker := xonk.Oonker
	if oonker == "" {
		oonker = xonk.Honker
	}
	dt := time.Now().UTC()
	bonk := Honk{
		UserID:   userinfo.UserID,
		Username: userinfo.Username,
		What:     "bonk",
		Honker:   user.URL,
		Oonker:   oonker,
		XID:      xonk.XID,
		RID:      xonk.RID,
		Noise:    xonk.Noise,
		Precis:   xonk.Precis,
		URL:      xonk.URL,
		Date:     dt,
		Donks:    xonk.Donks,
		Whofore:  2,
		Convoy:   xonk.Convoy,
		Audience: []string{thewholeworld, oonker},
		Public:   true,
	}

	bonk.Format = "html"

	err = savehonk(&bonk)
	if err != nil {
		log.Printf("uh oh")
		return
	}

	go honkworldwide(user, &bonk)
}

func sendzonkofsorts(xonk *Honk, user *WhatAbout, what string) {
	zonk := Honk{
		What:     what,
		XID:      xonk.XID,
		Date:     time.Now().UTC(),
		Audience: oneofakind(xonk.Audience),
	}
	zonk.Public = !keepitquiet(zonk.Audience)

	log.Printf("announcing %sed honk: %s", what, xonk.XID)
	go honkworldwide(user, &zonk)
}

func zonkit(w http.ResponseWriter, r *http.Request) {
	wherefore := r.FormValue("wherefore")
	what := r.FormValue("what")
	userinfo := login.GetUserInfo(r)
	user, _ := butwhatabout(userinfo.Username)

	if wherefore == "ack" {
		xonk := getxonk(userinfo.UserID, what)
		if xonk != nil {
			_, err := stmtUpdateFlags.Exec(flagIsAcked, xonk.ID)
			if err != nil {
				log.Printf("error acking: %s", err)
			}
			sendzonkofsorts(xonk, user, "ack")
		}
		return
	}

	if wherefore == "deack" {
		xonk := getxonk(userinfo.UserID, what)
		if xonk != nil {
			_, err := stmtClearFlags.Exec(flagIsAcked, xonk.ID)
			if err != nil {
				log.Printf("error deacking: %s", err)
			}
			sendzonkofsorts(xonk, user, "deack")
		}
		return
	}

	if wherefore == "unbonk" {
		xonk := getbonk(userinfo.UserID, what)
		if xonk != nil {
			deletehonk(xonk.ID)
			xonk = getxonk(userinfo.UserID, what)
			_, err := stmtClearFlags.Exec(flagIsBonked, xonk.ID)
			if err != nil {
				log.Printf("error unbonking: %s", err)
			}
			sendzonkofsorts(xonk, user, "unbonk")
		}
		return
	}

	log.Printf("zonking %s %s", wherefore, what)
	if wherefore == "zonk" {
		xonk := getxonk(userinfo.UserID, what)
		if xonk != nil {
			deletehonk(xonk.ID)
			if xonk.Whofore == 2 || xonk.Whofore == 3 {
				sendzonkofsorts(xonk, user, "zonk")
			}
		}
	}
	_, err := stmtSaveZonker.Exec(userinfo.UserID, what, wherefore)
	if err != nil {
		log.Printf("error saving zonker: %s", err)
		return
	}
}

func edithonkpage(w http.ResponseWriter, r *http.Request) {
	u := login.GetUserInfo(r)
	user, _ := butwhatabout(u.Username)
	xid := r.FormValue("xid")
	honk := getxonk(u.UserID, xid)
	if honk == nil || honk.Honker != user.URL || honk.What != "honk" {
		log.Printf("no edit")
		return
	}

	noise := honk.Noise
	if honk.Precis != "" {
		noise = honk.Precis + "\n\n" + noise
	}

	honks := []*Honk{honk}
	donksforhonks(honks)
	reverbolate(u.UserID, honks)
	templinfo := getInfo(r)
	templinfo["HonkCSRF"] = login.GetCSRF("honkhonk", r)
	templinfo["Honks"] = honks
	templinfo["Noise"] = noise
	templinfo["ServerMessage"] = "honk edit"
	templinfo["UpdateXID"] = honk.XID
	if len(honk.Donks) > 0 {
		templinfo["SavedFile"] = honk.Donks[0].XID
	}
	err := readviews.Execute(w, "honkpage.html", templinfo)
	if err != nil {
		log.Print(err)
	}
}

// what a hot mess this function is
func submithonk(w http.ResponseWriter, r *http.Request) {
	rid := r.FormValue("rid")
	noise := r.FormValue("noise")

	userinfo := login.GetUserInfo(r)
	user, _ := butwhatabout(userinfo.Username)

	dt := time.Now().UTC()
	updatexid := r.FormValue("updatexid")
	var honk *Honk
	if updatexid != "" {
		honk = getxonk(userinfo.UserID, updatexid)
		if honk == nil || honk.Honker != user.URL || honk.What != "honk" {
			log.Printf("not saving edit")
			return
		}
		honk.Date = dt
		honk.What = "update"
		honk.Format = "markdown"
	} else {
		xid := fmt.Sprintf("%s/%s/%s", user.URL, honkSep, xfiltrate())
		what := "honk"
		if rid != "" {
			what = "tonk"
		}
		honk = &Honk{
			UserID:   userinfo.UserID,
			Username: userinfo.Username,
			What:     what,
			Honker:   user.URL,
			XID:      xid,
			Date:     dt,
			Format:   "markdown",
		}
	}

	noise = hooterize(noise)
	honk.Noise = noise
	translate(honk)

	var convoy string
	if rid != "" {
		xonk := getxonk(userinfo.UserID, rid)
		if xonk != nil {
			if xonk.Public {
				honk.Audience = append(honk.Audience, xonk.Audience...)
			}
			convoy = xonk.Convoy
		} else {
			xonkaud, c := whosthere(rid)
			honk.Audience = append(honk.Audience, xonkaud...)
			convoy = c
		}
		for i, a := range honk.Audience {
			if a == thewholeworld {
				honk.Audience[0], honk.Audience[i] = honk.Audience[i], honk.Audience[0]
				break
			}
		}
		honk.RID = rid
	} else {
		honk.Audience = []string{thewholeworld}
	}
	if honk.Noise != "" && honk.Noise[0] == '@' {
		honk.Audience = append(grapevine(honk.Noise), honk.Audience...)
	} else {
		honk.Audience = append(honk.Audience, grapevine(honk.Noise)...)
	}

	if convoy == "" {
		convoy = "data:,electrichonkytonk-" + xfiltrate()
	}
	butnottooloud(honk.Audience)
	honk.Audience = oneofakind(honk.Audience)
	if len(honk.Audience) == 0 {
		log.Printf("honk to nowhere")
		http.Error(w, "honk to nowhere...", http.StatusNotFound)
		return
	}
	honk.Public = !keepitquiet(honk.Audience)
	honk.Convoy = convoy

	donkxid := r.FormValue("donkxid")
	if donkxid == "" {
		file, filehdr, err := r.FormFile("donk")
		if err == nil {
			var buf bytes.Buffer
			io.Copy(&buf, file)
			file.Close()
			data := buf.Bytes()
			xid := xfiltrate()
			var media, name string
			img, err := image.Vacuum(&buf, image.Params{MaxWidth: 2048, MaxHeight: 2048})
			if err == nil {
				data = img.Data
				format := img.Format
				media = "image/" + format
				if format == "jpeg" {
					format = "jpg"
				}
				name = xid + "." + format
				xid = name
			} else {
				maxsize := 100000
				if len(data) > maxsize {
					log.Printf("bad image: %s too much text: %d", err, len(data))
					http.Error(w, "didn't like your attachment", http.StatusUnsupportedMediaType)
					return
				}
				for i := 0; i < len(data); i++ {
					if data[i] < 32 && data[i] != '\t' && data[i] != '\r' && data[i] != '\n' {
						log.Printf("bad image: %s not text: %d", err, data[i])
						http.Error(w, "didn't like your attachment", http.StatusUnsupportedMediaType)
						return
					}
				}
				media = "text/plain"
				name = filehdr.Filename
				if name == "" {
					name = xid + ".txt"
				}
				xid += ".txt"
			}
			desc := r.FormValue("donkdesc")
			if desc == "" {
				desc = name
			}
			url := fmt.Sprintf("https://%s/d/%s", serverName, xid)
			res, err := stmtSaveFile.Exec(xid, name, desc, url, media, 1, data)
			if err != nil {
				log.Printf("unable to save image: %s", err)
				return
			}
			var d Donk
			d.FileID, _ = res.LastInsertId()
			honk.Donks = append(honk.Donks, &d)
			donkxid = d.XID
		}
	} else {
		xid := donkxid
		url := fmt.Sprintf("https://%s/d/%s", serverName, xid)
		var donk Donk
		row := stmtFindFile.QueryRow(url)
		err := row.Scan(&donk.FileID)
		if err == nil {
			honk.Donks = append(honk.Donks, &donk)
		} else {
			log.Printf("can't find file: %s", xid)
		}
	}
	herd := herdofemus(honk.Noise)
	for _, e := range herd {
		donk := savedonk(e.ID, e.Name, e.Name, "image/png", true)
		if donk != nil {
			donk.Name = e.Name
			honk.Donks = append(honk.Donks, donk)
		}
	}
	memetize(honk)

	if honk.Public {
		honk.Whofore = 2
	} else {
		honk.Whofore = 3
	}
	if r.FormValue("preview") == "preview" {
		honks := []*Honk{honk}
		reverbolate(userinfo.UserID, honks)
		templinfo := getInfo(r)
		templinfo["HonkCSRF"] = login.GetCSRF("honkhonk", r)
		templinfo["Honks"] = honks
		templinfo["InReplyTo"] = r.FormValue("rid")
		templinfo["Noise"] = r.FormValue("noise")
		templinfo["SavedFile"] = donkxid
		templinfo["ServerMessage"] = "honk preview"
		err := readviews.Execute(w, "honkpage.html", templinfo)
		if err != nil {
			log.Print(err)
		}
		return
	}
	honk.UserID = userinfo.UserID
	honk.RID = rid
	honk.Date = dt
	honk.Convoy = convoy

	// back to markdown
	honk.Noise = noise

	if updatexid != "" {
		updatehonk(honk)
	} else {
		err := savehonk(honk)
		if err != nil {
			log.Printf("uh oh")
			return
		}
	}

	// reload for consistency
	honk.Donks = nil
	donksforhonks([]*Honk{honk})

	go honkworldwide(user, honk)

	http.Redirect(w, r, honk.XID, http.StatusSeeOther)
}

func showhonkers(w http.ResponseWriter, r *http.Request) {
	userinfo := login.GetUserInfo(r)
	templinfo := getInfo(r)
	templinfo["Honkers"] = gethonkers(userinfo.UserID)
	templinfo["HonkerCSRF"] = login.GetCSRF("submithonker", r)
	err := readviews.Execute(w, "honkers.html", templinfo)
	if err != nil {
		log.Print(err)
	}
}

func showcombos(w http.ResponseWriter, r *http.Request) {
	userinfo := login.GetUserInfo(r)
	templinfo := getInfo(r)
	honkers := gethonkers(userinfo.UserID)
	var combos []string
	for _, h := range honkers {
		combos = append(combos, h.Combos...)
	}
	for i, c := range combos {
		if c == "-" {
			combos[i] = ""
		}
	}
	combos = oneofakind(combos)
	sort.Strings(combos)
	templinfo["Combos"] = combos
	err := readviews.Execute(w, "combos.html", templinfo)
	if err != nil {
		log.Print(err)
	}
}

func submithonker(w http.ResponseWriter, r *http.Request) {
	u := login.GetUserInfo(r)
	name := r.FormValue("name")
	url := r.FormValue("url")
	peep := r.FormValue("peep")
	combos := r.FormValue("combos")
	honkerid, _ := strconv.ParseInt(r.FormValue("honkerid"), 10, 0)

	if honkerid > 0 {
		goodbye := r.FormValue("goodbye")
		if goodbye == "F" {
			db := opendatabase()
			row := db.QueryRow("select xid from honkers where honkerid = ? and userid = ?",
				honkerid, u.UserID)
			var xid string
			err := row.Scan(&xid)
			if err != nil {
				log.Printf("can't get honker xid: %s", err)
				return
			}
			log.Printf("unsubscribing from %s", xid)
			user, _ := butwhatabout(u.Username)
			go itakeitallback(user, xid)
			_, err = stmtUpdateFlavor.Exec("unsub", u.UserID, xid, "sub")
			if err != nil {
				log.Printf("error updating honker: %s", err)
				return
			}

			http.Redirect(w, r, "/honkers", http.StatusSeeOther)
			return
		}
		combos = " " + strings.TrimSpace(combos) + " "
		_, err := stmtUpdateCombos.Exec(combos, honkerid, u.UserID)
		if err != nil {
			log.Printf("update honker err: %s", err)
			return
		}
		http.Redirect(w, r, "/honkers", http.StatusSeeOther)
	}

	flavor := "presub"
	if peep == "peep" {
		flavor = "peep"
	}
	p, err := investigate(url)
	if err != nil {
		http.Error(w, "error investigating: "+err.Error(), http.StatusInternalServerError)
		log.Printf("failed to investigate honker")
		return
	}
	url = p.XID
	if name == "" {
		name = p.Handle
	}
	_, err = stmtSaveHonker.Exec(u.UserID, name, url, flavor, combos)
	if err != nil {
		log.Print(err)
		return
	}
	if flavor == "presub" {
		user, _ := butwhatabout(u.Username)
		go subsub(user, url)
	}
	http.Redirect(w, r, "/honkers", http.StatusSeeOther)
}

func zonkzone(w http.ResponseWriter, r *http.Request) {
	userinfo := login.GetUserInfo(r)
	rows, err := stmtGetZonkers.Query(userinfo.UserID)
	if err != nil {
		log.Printf("err: %s", err)
		return
	}
	defer rows.Close()
	var zonkers []Zonker
	for rows.Next() {
		var z Zonker
		rows.Scan(&z.ID, &z.Name, &z.Wherefore)
		zonkers = append(zonkers, z)
	}
	sort.Slice(zonkers, func(i, j int) bool {
		w1 := zonkers[i].Wherefore
		w2 := zonkers[j].Wherefore
		if w1 == w2 {
			return zonkers[i].Name < zonkers[j].Name
		}
		if w1 == "zonvoy" {
			w1 = "zzzzzzz"
		}
		if w2 == "zonvoy" {
			w2 = "zzzzzzz"
		}
		return w1 < w2
	})

	templinfo := getInfo(r)
	templinfo["Zonkers"] = zonkers
	templinfo["ZonkCSRF"] = login.GetCSRF("zonkzonk", r)
	err = readviews.Execute(w, "zonkers.html", templinfo)
	if err != nil {
		log.Print(err)
	}
}

func zonkzonk(w http.ResponseWriter, r *http.Request) {
	userinfo := login.GetUserInfo(r)
	itsok := r.FormValue("itsok")
	if itsok == "iforgiveyou" {
		zonkerid, _ := strconv.ParseInt(r.FormValue("zonkerid"), 10, 0)
		db := opendatabase()
		db.Exec("delete from zonkers where userid = ? and zonkerid = ?",
			userinfo.UserID, zonkerid)
		bitethethumbs()
		http.Redirect(w, r, "/zonkzone", http.StatusSeeOther)
		return
	}
	wherefore := r.FormValue("wherefore")
	name := r.FormValue("name")
	if name == "" {
		return
	}
	switch wherefore {
	case "zonker":
	case "zomain":
	case "zonvoy":
	case "zord":
	case "zilence":
	default:
		return
	}
	db := opendatabase()
	db.Exec("insert into zonkers (userid, name, wherefore) values (?, ?, ?)",
		userinfo.UserID, name, wherefore)
	if wherefore == "zonker" || wherefore == "zomain" || wherefore == "zord" || wherefore == "zilence" {
		bitethethumbs()
	}

	http.Redirect(w, r, "/zonkzone", http.StatusSeeOther)
}

func accountpage(w http.ResponseWriter, r *http.Request) {
	u := login.GetUserInfo(r)
	user, _ := butwhatabout(u.Username)
	templinfo := getInfo(r)
	templinfo["UserCSRF"] = login.GetCSRF("saveuser", r)
	templinfo["LogoutCSRF"] = login.GetCSRF("logout", r)
	templinfo["User"] = user
	err := readviews.Execute(w, "account.html", templinfo)
	if err != nil {
		log.Print(err)
	}
}

func dochpass(w http.ResponseWriter, r *http.Request) {
	err := login.ChangePassword(w, r)
	if err != nil {
		log.Printf("error changing password: %s", err)
	}
	http.Redirect(w, r, "/account", http.StatusSeeOther)
}

func fingerlicker(w http.ResponseWriter, r *http.Request) {
	orig := r.FormValue("resource")

	log.Printf("finger lick: %s", orig)

	if strings.HasPrefix(orig, "acct:") {
		orig = orig[5:]
	}

	name := orig
	idx := strings.LastIndexByte(name, '/')
	if idx != -1 {
		name = name[idx+1:]
		if fmt.Sprintf("https://%s/%s/%s", serverName, userSep, name) != orig {
			log.Printf("foreign request rejected")
			name = ""
		}
	} else {
		idx = strings.IndexByte(name, '@')
		if idx != -1 {
			name = name[:idx]
			if name+"@"+serverName != orig {
				log.Printf("foreign request rejected")
				name = ""
			}
		}
	}
	user, err := butwhatabout(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	j := junk.New()
	j["subject"] = fmt.Sprintf("acct:%s@%s", user.Name, serverName)
	j["aliases"] = []string{user.URL}
	var links []junk.Junk
	l := junk.New()
	l["rel"] = "self"
	l["type"] = `application/activity+json`
	l["href"] = user.URL
	links = append(links, l)
	j["links"] = links

	w.Header().Set("Cache-Control", "max-age=3600")
	w.Header().Set("Content-Type", "application/jrd+json")
	j.Write(w)
}

func somedays() string {
	secs := 432000 + notrand.Int63n(432000)
	return fmt.Sprintf("%d", secs)
}

func avatate(w http.ResponseWriter, r *http.Request) {
	n := r.FormValue("a")
	a := avatar(n)
	w.Header().Set("Cache-Control", "max-age="+somedays())
	w.Write(a)
}

func servecss(w http.ResponseWriter, r *http.Request) {
	data, _ := ioutil.ReadFile("views" + r.URL.Path)
	s := css.Process(string(data))
	w.Header().Set("Cache-Control", "max-age=7776000")
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Write([]byte(s))
}
func servehtml(w http.ResponseWriter, r *http.Request) {
	templinfo := getInfo(r)
	err := readviews.Execute(w, r.URL.Path[1:]+".html", templinfo)
	if err != nil {
		log.Print(err)
	}
}
func serveemu(w http.ResponseWriter, r *http.Request) {
	xid := mux.Vars(r)["xid"]
	w.Header().Set("Cache-Control", "max-age="+somedays())
	http.ServeFile(w, r, "emus/"+xid)
}
func servememe(w http.ResponseWriter, r *http.Request) {
	xid := mux.Vars(r)["xid"]
	w.Header().Set("Cache-Control", "max-age="+somedays())
	http.ServeFile(w, r, "memes/"+xid)
}

func servefile(w http.ResponseWriter, r *http.Request) {
	xid := mux.Vars(r)["xid"]
	row := stmtFileData.QueryRow(xid)
	var media string
	var data []byte
	err := row.Scan(&media, &data)
	if err != nil {
		log.Printf("error loading file: %s", err)
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", media)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "max-age="+somedays())
	w.Write(data)
}

func nomoroboto(w http.ResponseWriter, r *http.Request) {
	io.WriteString(w, "User-agent: *\n")
	io.WriteString(w, "Disallow: /a\n")
	io.WriteString(w, "Disallow: /d\n")
	io.WriteString(w, "Disallow: /meme\n")
	for _, u := range allusers() {
		fmt.Fprintf(w, "Disallow: /%s/%s/%s/\n", userSep, u.Username, honkSep)
	}
}

func webhydra(w http.ResponseWriter, r *http.Request) {
	u := login.GetUserInfo(r)
	userid := u.UserID
	templinfo := getInfo(r)
	templinfo["HonkCSRF"] = login.GetCSRF("honkhonk", r)
	page := r.FormValue("page")
	var honks []*Honk
	switch page {
	case "atme":
		honks = gethonksforme(userid)
	case "home":
		honks = gethonksforuser(userid)
		honks = osmosis(honks, userid)
	case "convoy":
		c := r.FormValue("c")
		honks = gethonksbyconvoy(userid, c)
	default:
		http.NotFound(w, r)
	}
	if len(honks) > 0 {
		templinfo["TopXID"] = honks[0].XID
	}
	if topxid := r.FormValue("topxid"); topxid != "" {
		for i, h := range honks {
			if h.XID == topxid {
				honks = honks[0:i]
				break
			}
		}
		log.Printf("topxid %d frags", len(honks))
	}
	reverbolate(userid, honks)
	templinfo["Honks"] = honks
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	err := readviews.Execute(w, "honkfrags.html", templinfo)
	if err != nil {
		log.Printf("frag error: %s", err)
	}
}

func serve() {
	db := opendatabase()
	login.Init(db)

	listener, err := openListener()
	if err != nil {
		log.Fatal(err)
	}
	go redeliverator()

	debug := false
	getconfig("debug", &debug)
	readviews = templates.Load(debug,
		"views/honkpage.html",
		"views/honkfrags.html",
		"views/honkers.html",
		"views/zonkers.html",
		"views/combos.html",
		"views/honkform.html",
		"views/honk.html",
		"views/account.html",
		"views/about.html",
		"views/funzone.html",
		"views/login.html",
		"views/xzone.html",
		"views/header.html",
		"views/onts.html",
		"views/honkpage.js",
	)
	if !debug {
		s := "views/style.css"
		savedstyleparams[s] = getstyleparam(s)
		s = "views/local.css"
		savedstyleparams[s] = getstyleparam(s)
	}

	bitethethumbs()

	mux := mux.NewRouter()
	mux.Use(login.Checker)

	posters := mux.Methods("POST").Subrouter()
	getters := mux.Methods("GET").Subrouter()

	getters.HandleFunc("/", homepage)
	getters.HandleFunc("/home", homepage)
	getters.HandleFunc("/front", homepage)
	getters.HandleFunc("/robots.txt", nomoroboto)
	getters.HandleFunc("/rss", showrss)
	getters.HandleFunc("/"+userSep+"/{name:[[:alnum:]]+}", showuser)
	getters.HandleFunc("/"+userSep+"/{name:[[:alnum:]]+}/"+honkSep+"/{xid:[[:alnum:]]+}", showhonk)
	getters.HandleFunc("/"+userSep+"/{name:[[:alnum:]]+}/rss", showrss)
	posters.HandleFunc("/"+userSep+"/{name:[[:alnum:]]+}/inbox", inbox)
	getters.HandleFunc("/"+userSep+"/{name:[[:alnum:]]+}/outbox", outbox)
	getters.HandleFunc("/"+userSep+"/{name:[[:alnum:]]+}/followers", emptiness)
	getters.HandleFunc("/"+userSep+"/{name:[[:alnum:]]+}/following", emptiness)
	getters.HandleFunc("/a", avatate)
	getters.HandleFunc("/o", thelistingoftheontologies)
	getters.HandleFunc("/o/{name:.+}", showontology)
	getters.HandleFunc("/d/{xid:[[:alnum:].]+}", servefile)
	getters.HandleFunc("/emu/{xid:[[:alnum:]_.-]+}", serveemu)
	getters.HandleFunc("/meme/{xid:[[:alnum:]_.-]+}", servememe)
	getters.HandleFunc("/.well-known/webfinger", fingerlicker)

	getters.HandleFunc("/style.css", servecss)
	getters.HandleFunc("/local.css", servecss)
	getters.HandleFunc("/about", servehtml)
	getters.HandleFunc("/login", servehtml)
	posters.HandleFunc("/dologin", login.LoginFunc)
	getters.HandleFunc("/logout", login.LogoutFunc)

	loggedin := mux.NewRoute().Subrouter()
	loggedin.Use(login.Required)
	loggedin.HandleFunc("/account", accountpage)
	loggedin.HandleFunc("/funzone", showfunzone)
	loggedin.HandleFunc("/chpass", dochpass)
	loggedin.HandleFunc("/atme", homepage)
	loggedin.HandleFunc("/zonkzone", zonkzone)
	loggedin.HandleFunc("/xzone", xzone)
	loggedin.HandleFunc("/edit", edithonkpage)
	loggedin.Handle("/honk", login.CSRFWrap("honkhonk", http.HandlerFunc(submithonk)))
	loggedin.Handle("/bonk", login.CSRFWrap("honkhonk", http.HandlerFunc(submitbonk)))
	loggedin.Handle("/zonkit", login.CSRFWrap("honkhonk", http.HandlerFunc(zonkit)))
	loggedin.Handle("/zonkzonk", login.CSRFWrap("zonkzonk", http.HandlerFunc(zonkzonk)))
	loggedin.Handle("/saveuser", login.CSRFWrap("saveuser", http.HandlerFunc(saveuser)))
	loggedin.Handle("/ximport", login.CSRFWrap("ximport", http.HandlerFunc(ximport)))
	loggedin.HandleFunc("/honkers", showhonkers)
	loggedin.HandleFunc("/h/{name:[[:alnum:]]+}", showhonker)
	loggedin.HandleFunc("/h", showhonker)
	loggedin.HandleFunc("/c/{name:[[:alnum:]]+}", showcombo)
	loggedin.HandleFunc("/c", showcombos)
	loggedin.HandleFunc("/t", showconvoy)
	loggedin.HandleFunc("/q", showsearch)
	loggedin.HandleFunc("/hydra", webhydra)
	loggedin.Handle("/submithonker", login.CSRFWrap("submithonker", http.HandlerFunc(submithonker)))

	err = http.Serve(listener, mux)
	if err != nil {
		log.Fatal(err)
	}
}