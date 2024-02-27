package main

import (
	"bytes"
	notrand "math/rand"
	"strings"
	"time"

	"github.com/mmcdole/gofeed"
)

func syndicate(user *WhatAbout, url string) {
	data, err := fetchsome(url)
	if err != nil {
		dlog.Printf("error fetching feed: %s", err)
		return
	}
	parser := gofeed.NewParser()
	rss, err := parser.Parse(bytes.NewReader(data))
	if err != nil {
		dlog.Printf("error parsing feed: %s", err)
		return
	}
	reverseItems(rss.Items)
	for _, item := range rss.Items {
		var final string
		dlog.Printf("link: %s", item.Link)
		if x := getxonk(user.ID, item.Link); x != nil {
			dlog.Printf("already have it")
			continue
		}
		j, err := GetJunkTimeout(user.ID, item.Link, fastTimeout*time.Second, &final)
		if err != nil {
			dlog.Printf("unable to fetch link: %s", err)
			continue
		}
		xonksaver(user, j, originate(final))
	}
}

func syndicator() {
	for {
		dur := 12 * time.Hour
		dur += time.Duration(notrand.Int63n(int64(dur / 4)))
		time.Sleep(dur)
		users := allusers()
		for _, ui := range users {
			user, _ := butwhatabout(ui.Username)
			honkers := gethonkers(user.ID)
			for _, h := range honkers {
				if strings.HasSuffix(h.XID, ".rss") {
					syndicate(user, h.XID)
				}
			}
		}
	}
}

func reverseItems(items []*gofeed.Item) {
	for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
		items[i], items[j] = items[j], items[i]
	}
}
