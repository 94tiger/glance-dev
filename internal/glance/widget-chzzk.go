package glance

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"time"
)

var chzzkChannelsWidgetTemplate = mustParseTemplate("chzzk-channels.html", "widget-base.html")

type chzzkChannelsWidget struct {
	widgetBase      `yaml:",inline"`
	ChannelsRequest []string       `yaml:"channels"`
	Channels        []chzzkChannel `yaml:"-"`
	CollapseAfter   int            `yaml:"collapse-after"`
	SortBy          string         `yaml:"sort-by"`
	ClientId        string         `yaml:"client-id"`
	ClientSecret    string         `yaml:"client-secret"`
}

func (widget *chzzkChannelsWidget) initialize() error {
	widget.
		withTitle("Chzzk Channels").
		withTitleURL("https://chzzk.naver.com/home").
		withCacheDuration(time.Minute * 5)

	if widget.CollapseAfter == 0 || widget.CollapseAfter < -1 {
		widget.CollapseAfter = 5
	}

	if widget.SortBy != "followers" {
		widget.SortBy = "followers"
	}

	if widget.ClientId == "" || widget.ClientSecret == "" {
		return fmt.Errorf("Chzzk API requires client-id and client-secret")
	}

	return nil
}

func (widget *chzzkChannelsWidget) update(ctx context.Context) {
	channels, err := fetchChannelsFromChzzk(widget.ChannelsRequest, widget.ClientId, widget.ClientSecret)
	if !widget.canContinueUpdateAfterHandlingErr(err) {
		return
	}

	if widget.SortBy == "followers" {
		channels.sortByFollowers()
	}

	widget.Channels = channels
}

func (widget *chzzkChannelsWidget) Render() template.HTML {
	return widget.renderTemplate(widget, chzzkChannelsWidgetTemplate)
}

type chzzkChannel struct {
	ChannelId     string
	Name          string
	AvatarUrl     string
	FollowerCount int
}

type chzzkChannelList []chzzkChannel

func (channels chzzkChannelList) sortByFollowers() {
	sort.Slice(channels, func(i, j int) bool {
		return channels[i].FollowerCount > channels[j].FollowerCount
	})
}

const chzzkBaseURL = "https://openapi.chzzk.naver.com"

type chzzkChannelResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Content struct {
		Data []struct {
			ChannelId       string `json:"channelId"`
			ChannelName     string `json:"channelName"`
			ChannelImageUrl string `json:"channelImageUrl"`
			FollowerCount   int    `json:"followerCount"`
		} `json:"data"`
	} `json:"content"`
}

func fetchChannelsFromChzzk(channelIds []string, clientId, clientSecret string) (chzzkChannelList, error) {
	result := make(chzzkChannelList, 0, len(channelIds))

	slog.Info("Fetching Chzzk channels", "channelIds", channelIds, "clientId", clientId)

	for i := 0; i < len(channelIds); i += 20 {
		end := i + 20
		if end > len(channelIds) {
			end = len(channelIds)
		}

		batch := channelIds[i:end]
		slog.Info("Fetching batch of Chzzk channels", "batch", batch)

		channels, err := fetchChannelBatch(batch, clientId, clientSecret)
		if err != nil {
			slog.Error("Failed to fetch batch of Chzzk channels", "error", err, "batch", batch)
			continue
		}

		slog.Info("Successfully fetched batch of Chzzk channels", "count", len(channels))

		result = append(result, channels...)
	}

	if len(result) == 0 {
		slog.Error("No Chzzk channels fetched", "requestedCount", len(channelIds))
		return result, errNoContent
	}

	if len(result) < len(channelIds) {
		slog.Warn("Partially fetched Chzzk channels", "fetched", len(result), "requested", len(channelIds))
		return result, fmt.Errorf("%w: failed to fetch %d channels", errPartialContent, len(channelIds)-len(result))
	}

	slog.Info("Successfully fetched all Chzzk channels", "count", len(result))
	return result, nil
}

func fetchChannelBatch(channelIds []string, clientId, clientSecret string) (chzzkChannelList, error) {
	result := make(chzzkChannelList, 0, len(channelIds))

	baseURL := fmt.Sprintf("%s/open/v1/channels", chzzkBaseURL)
	params := url.Values{}
	for _, id := range channelIds {
		params.Add("channelIds", id)
	}

	apiURL := fmt.Sprintf("%s?%s", baseURL, params.Encode())
	slog.Info("Making API request to Chzzk", "url", apiURL)

	request, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return result, fmt.Errorf("creating request: %w", err)
	}

	request.Header.Add("Client-Id", clientId)
	request.Header.Add("Client-Secret", clientSecret)
	request.Header.Add("Content-Type", "application/json")

	response, err := defaultHTTPClient.Do(request)
	if err != nil {
		slog.Error("HTTP request failed", "error", err, "url", apiURL)
		return result, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer response.Body.Close()

	body, err := io.ReadAll(response.Body)
	if err != nil {
		slog.Error("Failed to read response body", "error", err)
		return result, fmt.Errorf("reading response body: %w", err)
	}

	slog.Info("API response body", "body", string(body))

	var apiResponse chzzkChannelResponse
	if err := json.Unmarshal(body, &apiResponse); err != nil {
		slog.Error("Failed to parse JSON response", "error", err, "body", string(body))
		return result, fmt.Errorf("parsing JSON response: %w", err)
	}

	if apiResponse.Code != 200 {
		slog.Error("API error response", "code", apiResponse.Code, "message", apiResponse.Message)
		return result, fmt.Errorf("API error: %s (code: %d)", apiResponse.Message, apiResponse.Code)
	}

	slog.Info("API response received", "code", apiResponse.Code, "channelCount", len(apiResponse.Content.Data))

	for _, channel := range apiResponse.Content.Data {
		result = append(result, chzzkChannel{
			ChannelId:     channel.ChannelId,
			Name:          channel.ChannelName,
			AvatarUrl:     channel.ChannelImageUrl,
			FollowerCount: channel.FollowerCount,
		})
	}

	return result, nil
}
