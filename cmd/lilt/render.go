package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/3-tiao/lilt/internal/api"
	"github.com/3-tiao/lilt/internal/presentation"
)

// renderHuman prints a success envelope as readable text. Commands that carry
// no payload keep the terse historical "ok"; everything else renders its data
// so a human does not need --json to see what happened.
func renderHuman(command string, subcommand string, data json.RawMessage) string {
	if len(data) == 0 || string(data) == "null" {
		return ""
	}
	var payload any
	if err := json.Unmarshal(data, &payload); err != nil {
		// Unknown shape: fall back to the compact JSON, still better than "ok".
		return string(data)
	}
	switch command {
	case "sources":
		return renderSources(payload)
	case "status":
		return renderStatus(payload)
	case "search", "trending":
		return renderSearch(payload)
	case "playlist":
		return renderPlaylistTracks(payload)
	case "album":
		return renderAlbumTracks(payload)
	case "albums":
		return renderItems(payload)
	case "library", "favorites", "recent":
		return renderItems(payload)
	case "favorite":
		return renderFavoriteResult(payload)
	case "history":
		return renderHistory(subcommand, payload)
	case "data":
		return renderDataReset(payload)
	case "queue":
		return renderQueue(payload)
	case "radio":
		return renderRadio(subcommand, payload)
	case "auth":
		return renderAuth(payload)
	default:
		return ""
	}
}

func renderFavoriteResult(payload any) string {
	result, ok := payload.(map[string]any)
	if !ok {
		return ""
	}
	favorited, _ := result["favorited"].(bool)
	verb := "Unfavorited"
	if favorited {
		verb = "★ Favorited"
	}
	if item, ok := result["item"].(map[string]any); ok {
		if title, _ := item["title"].(string); title != "" {
			return fmt.Sprintf("%s: %s\n", verb, presentation.Text(title))
		}
	}
	return verb + "\n"
}

func renderHistory(subcommand string, payload any) string {
	switch subcommand {
	case "stats":
		entries, ok := payload.([]any)
		if !ok {
			return ""
		}
		var b strings.Builder
		for _, entry := range entries {
			stat, ok := entry.(map[string]any)
			if !ok {
				continue
			}
			ref, _ := stat["ref"].(string)
			count, _ := stat["playCount"].(float64)
			fmt.Fprintf(&b, "%-52s %d\n", ref, int(count))
			if last, ok := stat["lastPlayedAt"].(string); ok && last != "" {
				fmt.Fprintf(&b, "    last: %s\n", last)
			}
		}
		return b.String()
	case "clear":
		result, ok := payload.(map[string]any)
		if !ok {
			return ""
		}
		cleared, _ := result["cleared"].(float64)
		return fmt.Sprintf("Cleared %d history entries\n", int(cleared))
	default:
		result, ok := payload.(map[string]any)
		if !ok {
			return ""
		}
		entries, _ := result["entries"].([]any)
		if len(entries) == 0 {
			return "(no history)\n"
		}
		var b strings.Builder
		for _, entry := range entries {
			e, ok := entry.(map[string]any)
			if !ok {
				continue
			}
			playedAt, _ := e["playedAt"].(string)
			if item, ok := e["item"].(map[string]any); ok {
				title, _ := item["title"].(string)
				artist, _ := item["artist"].(string)
				line := fmt.Sprintf("%s  %s", playedAt, presentation.Text(title))
				if artist != "" {
					line += " — " + presentation.Text(artist)
				}
				fmt.Fprintln(&b, line)
				if ref, ok := item["ref"].(string); ok && ref != "" {
					fmt.Fprintf(&b, "    %s\n", ref)
				}
			}
		}
		if next, ok := result["nextCursor"].(string); ok && next != "" {
			fmt.Fprintf(&b, "next: --before %s\n", next)
		}
		return b.String()
	}
}

func renderDataReset(payload any) string {
	result, ok := payload.(map[string]any)
	if !ok {
		return ""
	}
	archived, _ := result["archived"].(bool)
	line := "Activity database reset"
	if archived {
		line += " (previous database archived"
		if path, ok := result["archivePath"].(string); ok && path != "" {
			line += " at " + path
		}
		line += ")"
	}
	return line + "\n"
}

func renderSources(payload any) string {
	descriptors, ok := payload.([]any)
	if !ok {
		return ""
	}
	var b strings.Builder
	for _, entry := range descriptors {
		descriptor, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		id, _ := descriptor["id"].(string)
		available, _ := descriptor["available"].(bool)
		mark := "✓"
		if !available {
			mark = "✗"
		}
		caps := []string{}
		if capabilities, ok := descriptor["capabilities"].(map[string]any); ok {
			for name, raw := range capabilities {
				capability, ok := raw.(map[string]any)
				if !ok {
					continue
				}
				if enabled, _ := capability["available"].(bool); enabled {
					caps = append(caps, name)
				}
			}
		}
		sort.Strings(caps)
		fmt.Fprintf(&b, "%s %-12s %s\n", mark, id, strings.Join(caps, ", "))
	}
	return b.String()
}

func formatDuration(seconds float64) string {
	if seconds <= 0 {
		return "0:00"
	}
	total := int(seconds)
	return fmt.Sprintf("%d:%02d", total/60, total%60)
}

func renderStatus(payload any) string {
	state, ok := payload.(map[string]any)
	if !ok {
		return ""
	}
	var b strings.Builder
	status, _ := state["status"].(string)
	source, _ := state["source"].(string)
	fmt.Fprintf(&b, "%-8s %s\n", "status:", status)
	fmt.Fprintf(&b, "%-8s %s\n", "source:", source)
	if track, ok := state["track"].(map[string]any); ok && track != nil {
		line, _ := itemLine(track, false)
		fmt.Fprintf(&b, "%-8s %s\n", "track:", strings.TrimSpace(line))
	}
	position, _ := state["position"].(float64)
	duration, _ := state["duration"].(float64)
	if status == "playing" || status == "paused" || status == "buffering" {
		fmt.Fprintf(&b, "%-8s %s / %s\n", "time:", formatDuration(position), formatDuration(duration))
	}
	if mode, _ := state["mode"].(string); mode != "" && mode != "none" {
		fmt.Fprintf(&b, "%-8s %s\n", "mode:", mode)
	}
	if format, _ := state["format"].(string); format != "" && format != "—" {
		fmt.Fprintf(&b, "%-8s %s\n", "format:", format)
	}
	if shuffle, _ := state["shuffle"].(bool); shuffle {
		fmt.Fprintln(&b, "shuffle:  on")
	}
	if repeat, _ := state["repeatMode"].(string); repeat != "" && repeat != "off" {
		fmt.Fprintf(&b, "%-8s %s\n", "repeat:", repeat)
	}
	if streamTitle, ok := state["streamTitle"].(string); ok && streamTitle != "" {
		fmt.Fprintf(&b, "%-8s %s\n", "stream:", streamTitle)
	}
	return b.String()
}

func renderSearch(payload any) string {
	result, ok := payload.(map[string]any)
	if !ok {
		return ""
	}
	var b strings.Builder
	if source, ok := result["source"].(string); ok && source != "" {
		fmt.Fprintf(&b, "source: %s\n", source)
	}
	groups, _ := result["groups"].(map[string]any)
	if groups == nil {
		return b.String()
	}
	// Radio search reuses this renderer via its items shape.
	if _, isRadio := groups["stations"]; !isRadio {
		if items, ok := result["items"].([]any); ok {
			return renderItemList(items)
		}
	}
	for _, key := range []string{api.GroupSongs, api.GroupAlbums, api.GroupPlaylists, api.GroupStations} {
		entries, ok := groups[key].([]any)
		if !ok || len(entries) == 0 {
			continue
		}
		fmt.Fprintf(&b, "── %s ──\n", strings.ToUpper(key[:1])+key[1:])
		for _, entry := range entries {
			item, ok := entry.(map[string]any)
			if !ok {
				continue
			}
			line, _ := itemLine(item, true)
			fmt.Fprintln(&b, line)
		}
	}
	return b.String()
}

func renderItems(payload any) string {
	entries, ok := payload.([]any)
	if !ok {
		return ""
	}
	if len(entries) == 0 {
		return "(none)\n"
	}
	return renderItemList(entries)
}

func renderItemList(entries []any) string {
	var b strings.Builder
	for _, entry := range entries {
		item, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		line, _ := itemLine(item, true)
		fmt.Fprintln(&b, line)
	}
	return b.String()
}

// itemLine renders one item row. withRef appends the canonical ref on its own
// line so a human can copy it into `lilt play <ref>`.
func itemLine(item map[string]any, withRef bool) (string, bool) {
	kind, _ := item["kind"].(string)
	title, _ := item["title"].(string)
	artist, _ := item["artist"].(string)
	ref, _ := item["ref"].(string)
	url, _ := item["url"].(string)
	if title == "" && ref == "" && url == "" {
		return "", false
	}
	var b strings.Builder
	if kind != "" && kind != "song" && kind != "stream" {
		fmt.Fprintf(&b, "%s  ", kind)
	}
	b.WriteString(presentation.Text(title))
	if artist != "" {
		fmt.Fprintf(&b, " — %s", artist)
	}
	if ref != "" {
		fmt.Fprintf(&b, "\n    %s", ref)
	} else if url != "" {
		fmt.Fprintf(&b, "\n    %s", url)
	}
	return b.String(), true
}

func renderQueue(payload any) string {
	queue, ok := payload.(map[string]any)
	if !ok {
		return ""
	}
	items, _ := queue["items"].([]any)
	index, _ := queue["index"].(float64)
	if len(items) == 0 {
		return "(queue empty)\n"
	}
	var b strings.Builder
	for i, entry := range items {
		item, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		title, _ := item["title"].(string)
		artist, _ := item["artist"].(string)
		cursor := "  "
		if int(index) == i {
			cursor = "▶ "
		}
		line := fmt.Sprintf("%s%2d  %s", cursor, i, title)
		if artist != "" {
			line += " — " + artist
		}
		fmt.Fprintln(&b, line)
	}
	return b.String()
}

func renderAlbumTracks(payload any) string {
	result, ok := payload.(map[string]any)
	if !ok {
		return ""
	}
	var b strings.Builder
	if album, ok := result["album"].(map[string]any); ok && album != nil {
		title, _ := album["title"].(string)
		fmt.Fprintf(&b, "album: %s\n", presentation.Text(title))
	}
	items, _ := result["items"].([]any)
	for i, entry := range items {
		item, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		title, _ := item["title"].(string)
		artist, _ := item["artist"].(string)
		if artist != "" {
			fmt.Fprintf(&b, "%3d  %s — %s\n", i+1, title, artist)
		} else {
			fmt.Fprintf(&b, "%3d  %s\n", i+1, title)
		}
	}
	return b.String()
}

func renderPlaylistTracks(payload any) string {
	result, ok := payload.(map[string]any)
	if !ok {
		return ""
	}
	var b strings.Builder
	if playlist, ok := result["playlist"].(map[string]any); ok && playlist != nil {
		title, _ := playlist["title"].(string)
		fmt.Fprintf(&b, "playlist: %s\n", presentation.Text(title))
	}
	items, _ := result["items"].([]any)
	for i, entry := range items {
		item, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		title, _ := item["title"].(string)
		artist, _ := item["artist"].(string)
		if artist != "" {
			fmt.Fprintf(&b, "%3d  %s — %s\n", i+1, title, artist)
		} else {
			fmt.Fprintf(&b, "%3d  %s\n", i+1, title)
		}
	}
	return b.String()
}

func renderRadio(subcommand string, payload any) string {
	switch subcommand {
	case "probe":
		result, ok := payload.(map[string]any)
		if !ok {
			return ""
		}
		status, _ := result["status"].(string)
		line := "probe: " + status
		if latency, ok := result["latencyMs"].(float64); ok && latency > 0 {
			line += fmt.Sprintf(" (%dms)", int(latency))
		}
		if code, ok := result["errorCode"].(string); ok && code != "" {
			line += " " + code
		}
		if message, ok := result["message"].(string); ok && message != "" {
			line += " " + message
		}
		return line + "\n"
	case "options":
		result, ok := payload.(map[string]any)
		if !ok {
			return ""
		}
		var b strings.Builder
		options, _ := result["options"].([]any)
		for _, entry := range options {
			option, ok := entry.(map[string]any)
			if !ok {
				continue
			}
			value, _ := option["value"].(string)
			count, _ := option["count"].(float64)
			fmt.Fprintf(&b, "%-24s %d\n", value, int(count))
		}
		return b.String()
	default:
		result, ok := payload.(map[string]any)
		if !ok {
			return ""
		}
		items, _ := result["items"].([]any)
		if items == nil {
			return ""
		}
		return renderItemList(items)
	}
}

func renderAuth(payload any) string {
	switch entries := payload.(type) {
	case []any:
		var b strings.Builder
		for _, entry := range entries {
			auth, ok := entry.(map[string]any)
			if !ok {
				continue
			}
			source, _ := auth["source"].(string)
			status, _ := auth["status"].(string)
			fmt.Fprintf(&b, "%-12s %s\n", source, status)
		}
		return b.String()
	case map[string]any:
		source, _ := entries["source"].(string)
		status, _ := entries["status"].(string)
		return fmt.Sprintf("%-12s %s\n", source, status)
	default:
		return ""
	}
}
