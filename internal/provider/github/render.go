package github

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"net/url"
	"strings"
	"time"

	"veri/internal/core"
	"veri/internal/textnorm"
)

const (
	maxCommentsPerThread = 30
	maxCommitPatchFiles  = 15
	maxPatchRunesPerFile = 2000
)

type discussionDoc struct {
	title     string
	state     string
	category  string
	answered  bool
	branches  string
	author    string
	createdAt string
	labels    []string
	body      string
}

func (p *Provider) renderThreadOutcome(ctx context.Context, origURL string, rt parsedRoute, sourceType, reqID string, noStore bool, doc discussionDoc) (*core.FetchOutcome, error) {
	commentsEndpoint := fmt.Sprintf("%s/repos/%s/%s/issues/%d/comments?per_page=%d",
		p.apiBaseURL, url.PathEscape(rt.owner), url.PathEscape(rt.repo), rt.number, maxCommentsPerThread+1)
	if sourceType == core.SourceGitHubDiscussion {
		commentsEndpoint = fmt.Sprintf("%s/repos/%s/%s/discussions/%d/comments?per_page=%d",
			p.apiBaseURL, url.PathEscape(rt.owner), url.PathEscape(rt.repo), rt.number, maxCommentsPerThread+1)
	}

	var comments []commentWire
	if cRes, cErr := p.doGitHubGET(ctx, p.apiClient, commentsEndpoint, "application/vnd.github+json"); cErr == nil {
		if cRes.noStore {
			noStore = true
		}
		_ = json.Unmarshal(cRes.body, &comments)
	}
	moreComments := len(comments) > maxCommentsPerThread
	if moreComments {
		comments = comments[:maxCommentsPerThread]
	}

	var reviewComments []commentWire
	moreReviews := false
	if sourceType == core.SourceGitHubPR {
		rcURL := fmt.Sprintf("%s/repos/%s/%s/pulls/%d/comments?per_page=%d",
			p.apiBaseURL, url.PathEscape(rt.owner), url.PathEscape(rt.repo), rt.number, maxCommentsPerThread+1)
		if rcRes, rcErr := p.doGitHubGET(ctx, p.apiClient, rcURL, "application/vnd.github+json"); rcErr == nil {
			if rcRes.noStore {
				noStore = true
			}
			_ = json.Unmarshal(rcRes.body, &reviewComments)
		}
		if len(reviewComments) > maxCommentsPerThread {
			moreReviews = true
			reviewComments = reviewComments[:maxCommentsPerThread]
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# %s (#%d)\n\n", textnorm.CleanInline(doc.title), rt.number)
	fmt.Fprintf(&b, "- **Repository**: %s/%s\n", rt.owner, rt.repo)
	if doc.state != "" {
		fmt.Fprintf(&b, "- **State**: %s\n", doc.state)
	}
	if doc.category != "" {
		fmt.Fprintf(&b, "- **Category**: %s\n", textnorm.CleanInline(doc.category))
	}
	if doc.answered {
		b.WriteString("- **Answered**: yes\n")
	}
	if doc.branches != "" {
		fmt.Fprintf(&b, "- **Branches**: %s\n", doc.branches)
	}
	if doc.author != "" {
		fmt.Fprintf(&b, "- **Author**: @%s\n", doc.author)
	}
	if doc.createdAt != "" {
		fmt.Fprintf(&b, "- **Created**: %s\n", doc.createdAt)
	}
	if len(doc.labels) > 0 {
		fmt.Fprintf(&b, "- **Labels**: %s\n", strings.Join(doc.labels, ", "))
	}
	b.WriteString("\n## Description\n\n")
	if bodyText := textnorm.CleanPlainText(doc.body); bodyText != "" {
		b.WriteString(bodyText)
	} else {
		b.WriteString("_(No description provided)_")
	}

	if len(comments) > 0 {
		b.WriteString("\n\n## Comments\n")
		for _, c := range comments {
			cText := textnorm.CleanPlainText(c.Body)
			if cText == "" {
				continue
			}
			fmt.Fprintf(&b, "\n### @%s (%s)\n\n%s\n", c.User.Login, c.CreatedAt, cText)
		}
		if moreComments {
			b.WriteString("\n_(Additional comments omitted)_\n")
		}
	}

	if len(reviewComments) > 0 {
		b.WriteString("\n\n## Inline Code Review Comments\n")
		for _, rc := range reviewComments {
			rcText := textnorm.CleanPlainText(rc.Body)
			if rcText == "" {
				continue
			}
			loc := rc.Path
			if rc.Line > 0 && loc != "" {
				loc = fmt.Sprintf("%s:%d", loc, rc.Line)
			}
			if loc != "" {
				fmt.Fprintf(&b, "\n### @%s on `%s` (%s)\n\n%s\n", rc.User.Login, loc, rc.CreatedAt, rcText)
			} else {
				fmt.Fprintf(&b, "\n### @%s (%s)\n\n%s\n", rc.User.Login, rc.CreatedAt, rcText)
			}
		}
		if moreReviews {
			b.WriteString("\n_(Additional review comments omitted)_\n")
		}
	}

	pub := textnorm.ParseFlexibleTime(doc.createdAt, time.UTC)
	title := fmt.Sprintf("%s/%s#%d: %s", rt.owner, rt.repo, rt.number, textnorm.CleanInline(doc.title))
	out := newOutcome(origURL, title, pub, strings.TrimSpace(b.String()), sourceType, reqID, noStore)
	out.Page.Author = doc.author
	out.Page.Site = "GitHub"
	return out, nil
}

func (p *Provider) fetchDiscussion(ctx context.Context, origURL string, rt parsedRoute) (*core.FetchOutcome, error) {
	discURL := fmt.Sprintf("%s/repos/%s/%s/discussions/%d", p.apiBaseURL, url.PathEscape(rt.owner), url.PathEscape(rt.repo), rt.number)
	res, err := p.doGitHubGET(ctx, p.apiClient, discURL, "application/vnd.github+json")
	if err != nil {
		return nil, err
	}
	var disc discussionWire
	if err := json.Unmarshal(res.body, &disc); err != nil {
		return nil, core.WrapError(core.CodeUpstreamBadResponse, "failed to decode GitHub discussion JSON", err).WithProvider(ProviderName)
	}
	var labels []string
	for _, l := range disc.Labels {
		if l.Name != "" {
			labels = append(labels, l.Name)
		}
	}
	return p.renderThreadOutcome(ctx, origURL, rt, core.SourceGitHubDiscussion, res.reqID, res.noStore, discussionDoc{
		title:     disc.Title,
		state:     disc.State,
		category:  disc.Category.Name,
		answered:  strings.TrimSpace(disc.AnswerURL) != "",
		author:    disc.User.Login,
		createdAt: disc.CreatedAt,
		labels:    labels,
		body:      disc.Body,
	})
}

func (p *Provider) fetchCommit(ctx context.Context, origURL string, rt parsedRoute) (*core.FetchOutcome, error) {
	commitURL := fmt.Sprintf("%s/repos/%s/%s/commits/%s", p.apiBaseURL, url.PathEscape(rt.owner), url.PathEscape(rt.repo), url.PathEscape(rt.sha))
	res, err := p.doGitHubGET(ctx, p.apiClient, commitURL, "application/vnd.github+json")
	if err != nil {
		return nil, err
	}
	var cm commitWire
	if err := json.Unmarshal(res.body, &cm); err != nil {
		return nil, core.WrapError(core.CodeUpstreamBadResponse, "failed to decode GitHub commit JSON", err).WithProvider(ProviderName)
	}

	shortSHA := cm.SHA
	if shortSHA == "" {
		shortSHA = rt.sha
	}
	if len(shortSHA) > 12 {
		shortSHA = shortSHA[:12]
	}
	subject, restMsg, _ := strings.Cut(textnorm.CleanPlainText(cm.Commit.Message), "\n")
	subject = textnorm.CleanInline(subject)
	if subject == "" {
		subject = "Commit " + shortSHA
	}
	author := cm.Author.Login
	if author == "" {
		author = textnorm.CleanInline(cm.Commit.Author.Name)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# %s (`%s`)\n\n", subject, shortSHA)
	fmt.Fprintf(&b, "- **Repository**: %s/%s\n", rt.owner, rt.repo)
	if author != "" {
		fmt.Fprintf(&b, "- **Author**: %s\n", author)
	}
	if cm.Commit.Author.Date != "" {
		fmt.Fprintf(&b, "- **Date**: %s\n", cm.Commit.Author.Date)
	}
	fmt.Fprintf(&b, "- **Changes**: %d files (+%d / -%d)\n", len(cm.Files), cm.Stats.Additions, cm.Stats.Deletions)

	if trimmedRest := strings.TrimSpace(restMsg); trimmedRest != "" {
		b.WriteString("\n## Message\n\n")
		b.WriteString(trimmedRest)
		b.WriteByte('\n')
	}

	if len(cm.Files) > 0 {
		b.WriteString("\n## Files\n")
		shown := min(len(cm.Files), maxCommitPatchFiles)
		for i := range shown {
			f := cm.Files[i]
			fmt.Fprintf(&b, "\n### `%s` (%s, +%d / -%d)\n", f.Filename, f.Status, f.Additions, f.Deletions)
			if patch := textnorm.CleanPlainText(f.Patch); patch != "" {
				if textnorm.RuneCount(patch) > maxPatchRunesPerFile {
					patch = textnorm.TruncateRunes(patch, maxPatchRunesPerFile)
				}
				b.WriteString("\n```diff\n")
				b.WriteString(patch)
				b.WriteString("\n```\n")
			}
		}
		if len(cm.Files) > shown {
			fmt.Fprintf(&b, "\n_(%d additional changed files omitted)_\n", len(cm.Files)-shown)
		}
	}

	pub := textnorm.ParseFlexibleTime(cm.Commit.Author.Date, time.UTC)
	title := fmt.Sprintf("%s/%s@%s: %s", rt.owner, rt.repo, shortSHA, subject)
	out := newOutcome(origURL, title, pub, strings.TrimSpace(b.String()), core.SourceGitHubCommit, res.reqID, res.noStore)
	out.Page.Author = author
	out.Page.Site = "GitHub"
	return out, nil
}
