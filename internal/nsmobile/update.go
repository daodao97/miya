package nsmobile

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/nsgo-dev/nsgo/core"
	"golang.org/x/mod/semver"
)

const (
	mobileReleaseAPI = "https://api.github.com/repos/daodao97/miya/releases/latest"
	testFlightFeed   = "https://github.com/daodao97/miya/releases/download/miya-testflight/update-ios.json"
	testFlightURL    = "https://testflight.apple.com/join/8uEWJnDZ"
)

type mobileUpdate struct {
	version string
	url     string
}

func (m *App) checkMobileUpdate() {
	platform, info := runtime.GOOS, core.App()
	current := info.Version
	if (platform != "android" && platform != "ios") || !semver.IsValid("v"+current) || time.Now().Before(m.nextUpdateCheck) {
		return
	}
	m.nextUpdateCheck = time.Now().Add(2 * time.Hour)
	go func() {
		feed := mobileReleaseAPI
		if platform == "ios" {
			feed = testFlightFeed
		}
		client := &http.Client{Timeout: 12 * time.Second}
		resp, err := client.Get(feed)
		var update mobileUpdate
		if err == nil {
			defer resp.Body.Close()
			switch resp.StatusCode {
			case http.StatusOK:
				update, err = readMobileUpdate(resp.Body, platform, current, info.Build)
			case http.StatusNotFound:
				// The platform has no public update yet.
			default:
				err = fmt.Errorf("Miya update service returned HTTP %d", resp.StatusCode)
			}
		}
		m.cx.Post(func() {
			if err != nil {
				m.nextUpdateCheck = time.Now().Add(5 * time.Minute)
				return
			}
			if m.appUpdate != update {
				m.appUpdate = update
				m.change()
			}
		})
	}()
}

func readMobileUpdate(body io.Reader, platform, current, currentBuild string) (mobileUpdate, error) {
	decoder := json.NewDecoder(io.LimitReader(body, 256<<10))
	if platform == "ios" {
		var beta struct {
			Version   string    `json:"version"`
			Build     string    `json:"build"`
			ExpiresAt time.Time `json:"expiresAt"`
		}
		if err := decoder.Decode(&beta); err != nil {
			return mobileUpdate{}, err
		}
		if !beta.ExpiresAt.After(time.Now()) || !semver.IsValid("v"+beta.Version) || semver.Prerelease("v"+beta.Version) != "" {
			return mobileUpdate{}, nil
		}
		order := semver.Compare("v"+beta.Version, "v"+current)
		build, buildErr := strconv.ParseUint(beta.Build, 10, 64)
		installed, installedErr := strconv.ParseUint(currentBuild, 10, 64)
		if buildErr == nil && build > 0 && (order > 0 || order == 0 && installedErr == nil && build > installed) {
			return mobileUpdate{version: beta.Version, url: testFlightURL}, nil
		}
		return mobileUpdate{}, nil
	}

	var release struct {
		Tag        string `json:"tag_name"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
		Assets     []struct {
			Name  string `json:"name"`
			State string `json:"state"`
		} `json:"assets"`
	}
	if err := decoder.Decode(&release); err != nil {
		return mobileUpdate{}, err
	}
	tag := release.Tag
	if release.Draft || release.Prerelease || !semver.IsValid(tag) || semver.Prerelease(tag) != "" || semver.Compare(tag, "v"+current) <= 0 {
		return mobileUpdate{}, nil
	}
	version := strings.TrimPrefix(tag, "v")
	for _, name := range []string{"miya-" + version + ".apk", "miya-" + version + "-android-arm64.apk"} {
		for _, asset := range release.Assets {
			if asset.Name == name && asset.State == "uploaded" {
				return mobileUpdate{version: version, url: "https://github.com/daodao97/miya/releases/download/" + tag + "/" + name}, nil
			}
		}
	}
	return mobileUpdate{}, nil
}
