package devtools

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/media"
	"github.com/moment/moment/internal/runtimefs"
	sqlitestore "github.com/moment/moment/internal/storage/sqlite"
)

// ShowcaseScenarioName is the one catalog scenario dated from the clock
// rather than from the seed: a site an owner has kept for seven years, with
// every reader-facing state on it at least once, so a screen can be checked
// against real data without composing it by hand first.
const ShowcaseScenarioName = "site-showcase"

// ShowcaseCase is one thing the showcase built and where to find it. Keys are
// stable across builds; identifiers are not, because the site is dated from
// the clock and identifiers carry their time.
type ShowcaseCase struct {
	Key  string `json:"key"`
	ID   string `json:"id"`
	Path string `json:"path"`
	Note string `json:"note"`
}

// ShowcaseManifest is what a showcase build reports beyond its counts. It is
// how a script or an agent reaches a specific case without reading the site
// first: every hidden post, quote, card, member, and calendar edge is listed
// under a key that does not change between builds.
type ShowcaseManifest struct {
	AnchoredAt string         `json:"anchored_at"`
	Timezone   string         `json:"timezone"`
	Owner      string         `json:"owner"`
	Cases      []ShowcaseCase `json:"cases"`
}

// showcaseWhen dates one post. Recent posts are offsets from the anchor so
// the timeline shows every relative form (minutes, hours, days, a date);
// calendar posts are fixed local dates in a year relative to the anchor's,
// so the archive always has a year boundary, a leap day, and a busy month.
type showcaseWhen struct {
	ago        time.Duration
	fixed      bool
	yearOffset int
	month      time.Month
	day        int
	hour       int
	minute     int
	leapDay    bool
}

func ago(duration time.Duration) showcaseWhen { return showcaseWhen{ago: duration} }

func on(yearOffset int, month time.Month, day, hour, minute int) showcaseWhen {
	return showcaseWhen{fixed: true, yearOffset: yearOffset, month: month, day: day, hour: hour, minute: minute}
}

func leapDay(hour, minute int) showcaseWhen {
	return showcaseWhen{fixed: true, leapDay: true, hour: hour, minute: minute}
}

func (when showcaseWhen) at(anchor time.Time, zone *time.Location) time.Time {
	if !when.fixed {
		return anchor.Add(-when.ago)
	}
	local := anchor.In(zone)
	if when.leapDay {
		for year := local.Year(); year > local.Year()-8; year-- {
			candidate := time.Date(year, time.February, 29, when.hour, when.minute, 0, 0, zone)
			if candidate.Month() == time.February && candidate.Before(anchor) {
				return candidate
			}
		}
	}
	candidate := time.Date(local.Year()+when.yearOffset, when.month, when.day, when.hour, when.minute, 0, 0, zone)
	// A date in the anchor's own year can still be ahead of the anchor early
	// in that year; the case is then taken from the year before, so it stays
	// a real, past date rather than a post from the future.
	if !candidate.Before(anchor) {
		candidate = candidate.AddDate(-1, 0, 0)
	}
	return candidate
}

type showcaseMember struct {
	key         string
	provider    string
	providerID  string
	displayName string
	username    string
	bio         string
	email       string
	joined      showcaseWhen
	status      domain.AccountStatus
	avatar      *showcaseImage
	note        string
}

type showcaseComment struct {
	author   string
	after    time.Duration
	content  string
	root     int
	replyTo  int
	pinned   bool
	images   []showcaseImage
	location *domain.Location
}

type showcaseLink struct {
	card      domain.LinkCard
	thumbnail *showcaseImage
	favicon   *showcaseImage
}

type showcasePost struct {
	key        string
	when       showcaseWhen
	note       string
	content    string
	visibility domain.Visibility
	images     []showcaseImage
	alt        func(index int) *string
	location   *domain.Location
	link       *showcaseLink
	quotes     string
	edit       string
	editAfter  time.Duration
	repostedAt *showcaseWhen
	pinned     bool
	deleted    bool
	comments   []showcaseComment
}

const (
	showcaseOwnerUsername    = "xiaoman"
	showcaseOwnerDisplayName = "小满"
)

func root(author string, after time.Duration, content string) showcaseComment {
	return showcaseComment{author: author, after: after, content: content, root: -1, replyTo: -1}
}

func reply(author string, after time.Duration, rootIndex int, content string) showcaseComment {
	return showcaseComment{author: author, after: after, content: content, root: rootIndex, replyTo: -1}
}

func answer(author string, after time.Duration, rootIndex, target int, content string) showcaseComment {
	return showcaseComment{author: author, after: after, content: content, root: rootIndex, replyTo: target}
}

func text(value string) *string { return &value }

func showcaseMembers() []showcaseMember {
	aming := square(28, 0)
	aming.Name = "avatar-aming.png"
	lena := square(318, 0)
	lena.Name = "avatar-lena.png"
	return []showcaseMember{
		{key: "aming", provider: "github", providerID: "10021", displayName: "阿明", username: "aming", bio: "写 Go，也写点前端。", email: "aming@example.com", joined: on(-3, time.May, 12, 10, 0), status: domain.AccountActive, avatar: &aming, note: "活跃成员，有头像，评论最多"},
		{key: "lena", provider: "google", providerID: "118273645", displayName: "Lena", username: "lena", bio: "Product designer in Berlin. Coffee, type, trains.", email: "lena@example.com", joined: on(-1, time.March, 3, 9, 30), status: domain.AccountActive, avatar: &lena, note: "活跃成员，英文资料与评论"},
		{key: "xiaobei", provider: "github", providerID: "77310", displayName: "北纬三十一度的小北", username: "xiaobei", bio: "", email: "", joined: ago(61 * 24 * time.Hour), status: domain.AccountActive, note: "活跃成员，长显示名，无头像（首字母兜底）"},
		{key: "wanger", provider: "github", providerID: "99012", displayName: "王二", username: "wanger", bio: "", email: "wanger@example.com", joined: ago(2*24*time.Hour + 3*time.Hour), status: domain.AccountPending, note: "待审核账户，出现在站长的账户评审页"},
		{key: "spam", provider: "google", providerID: "55550001", displayName: "Best Deals 4U", username: "bestdeals4u", bio: "", email: "", joined: ago(20 * 24 * time.Hour), status: domain.AccountSuspended, note: "已停用账户"},
	}
}

func showcasePosts() []showcasePost {
	shanghai := &domain.Location{
		Name: "武康路", DisplayName: "武康路 · 徐汇区", CountryCode: "CN", Country: "中国", Region: "上海市", City: "上海市", District: "徐汇区",
		Category: "道路", Provider: "amap", ProviderPlaceID: "B0FFHW2N3P",
		Point: &domain.GeoPoint{LatitudeE7: 311_960_000, LongitudeE7: 1_214_360_000, System: domain.CoordinateGCJ02},
	}
	tokyo := &domain.Location{
		Name: "Shimokitazawa", DisplayName: "Shimokitazawa, Setagaya, Tokyo", CountryCode: "JP", Country: "Japan", Region: "Tokyo", City: "Setagaya",
		Category: "neighbourhood", Provider: "geoapify", ProviderPlaceID: "51a2b3c4d5e6f708",
		Point: &domain.GeoPoint{LatitudeE7: 356_610_000, LongitudeE7: 1_396_680_000, System: domain.CoordinateWGS84},
	}
	balcony := &domain.Location{Name: "家里的阳台", DisplayName: "家里的阳台", Provider: "manual"}
	berlin := &domain.Location{Name: "Café Oslo", DisplayName: "Café Oslo, Prenzlauer Berg, Berlin", CountryCode: "DE", Country: "Germany", City: "Berlin", District: "Prenzlauer Berg", Category: "cafe", Provider: "geoapify", ProviderPlaceID: "51f0e1d2c3b4a596"}
	reposted := func(when showcaseWhen) *showcaseWhen { return &when }

	articleThumb := thumbnail(205, 0)
	articleThumb.Name = "card-article.png"
	articleIcon := favicon(205)
	articleIcon.Name = "card-article-icon.png"
	youtubeThumb := thumbnail(0, 0)
	youtubeThumb.Name = "card-youtube.png"
	biliThumb := thumbnail(330, 0)
	biliThumb.Name = "card-bilibili.png"
	biliIcon := favicon(330)
	biliIcon.Name = "card-bilibili-icon.png"
	duration := func(seconds int) *int { return &seconds }
	count := func(value int64) *int64 { return &value }

	return []showcasePost{
		{key: "recent.text", when: ago(3 * time.Minute), note: "几分钟前的纯文字", content: "刚把发布按钮对齐到 X 的尺寸：36px 高、左右 16px、15/20 加粗，一像素都不差。"},
		{key: "recent.mention", when: ago(38 * time.Minute), note: "提及一位成员", content: "谢谢 @aming 昨天帮忙看那个焦点问题——Safari 里按钮真的不拿焦点，不是我眼花。"},
		{key: "recent.image-one", when: ago(2 * time.Hour), note: "单图（横幅）+ 三条评论含回复", content: "傍晚的苏州河，风把云吹成了一条。", images: []showcaseImage{landscape(200, 1)},
			comments: []showcaseComment{
				root("aming", 25*time.Minute, "这个颜色太好了，是原片吗？"),
				answer("owner", 40*time.Minute, 0, 0, "原片，只裁了一下。"),
				root("lena", 90*time.Minute, "Love the gradient here."),
			}},
		{key: "recent.image-four", when: ago(5 * time.Hour), note: "四图网格，四种比例", content: "周末拍的四张，比例各不一样。", images: []showcaseImage{landscape(20, 1), portrait(60, 2), square(110, 3), panorama(150, 4)}},
		{key: "recent.long-markdown", when: ago(11 * time.Hour), note: "长 Markdown：标题、列表、引用、代码、链接", content: showcaseLongMarkdown,
			comments: []showcaseComment{
				root("xiaobei", 2*time.Hour, showcaseLongComment),
				{author: "aming", after: 3 * time.Hour, content: "配一张我这边的截图。", root: -1, replyTo: -1, images: []showcaseImage{landscape(300, 1)}},
				{author: "lena", after: 5 * time.Hour, content: "Reading this from a café, exactly the place for it.", root: -1, replyTo: -1, location: berlin},
				reply("owner", 6*time.Hour, 0, "写太长了，下次拆开发。"),
			}},
		{key: "recent.hidden", when: ago(27 * time.Hour), note: "仅自己可见（带锁标；访客看不到）", visibility: domain.VisibilityOnlyMe, content: "这条只有我自己看得到：下周三体检，记得空腹，带上去年的报告。",
			comments: []showcaseComment{root("owner", 30*time.Minute, "备忘：报告在书房第二格抽屉。")}},
		{key: "recent.quote-public", when: ago(2 * 24 * time.Hour), note: "引用一条公开的图文帖", content: "翻到年初这组，现在看还是喜欢。", quotes: "year.photo-set"},
		{key: "recent.link-article", when: ago(3 * 24 * time.Hour), note: "链接卡片：文章，有缩略图与站点图标", content: "把 composer 的焦点问题写成了一篇笔记。",
			link: &showcaseLink{thumbnail: &articleThumb, favicon: &articleIcon, card: domain.LinkCard{
				SourceURL: "https://notes.example.net/2026/composer-focus", CanonicalURL: "https://notes.example.net/2026/composer-focus",
				Provider: domain.LinkProviderGeneric, Kind: domain.LinkKindLink,
				Title: "按下不动焦点：一个 composer 收起抖动的根因", Description: "Safari 点按钮不给它焦点，而每个浏览器点空白处都会让输入框失焦。只要 mousedown 上 preventDefault，两个问题一起消失。",
				SiteName: "小满的笔记", AuthorName: "小满",
			}},
			comments: []showcaseComment{
				root("aming", time.Hour, "已读，那个 preventDefault 的结论我也踩过。"),
				root("owner", 2*time.Hour, "有个地方写漏了：Firefox 会因此丢 :active，回头补。"),
			}},
		{key: "recent.link-video", when: ago(4 * 24 * time.Hour), note: "链接卡片：YouTube 视频（时长、播放量）", content: "这段讲字体渲染的视频值得看两遍。",
			link: &showcaseLink{thumbnail: &youtubeThumb, card: domain.LinkCard{
				SourceURL: "https://www.youtube.com/watch?v=aBcDeFgHiJk", CanonicalURL: "https://www.youtube.com/watch?v=aBcDeFgHiJk",
				Provider: domain.LinkProviderYouTube, Kind: domain.LinkKindVideo, EmbedURL: "https://www.youtube.com/embed/aBcDeFgHiJk",
				Title: "How Text Rendering Actually Works", Description: "From glyph outlines to subpixel positioning, in 25 minutes.", SiteName: "YouTube", AuthorName: "Type Tuesday",
				DurationSeconds: duration(1512), ViewCount: count(184_203), LikeCount: count(9_120), CommentCount: count(412),
			}}},
		{key: "recent.location", when: ago(6 * 24 * time.Hour), note: "带地点（高德）+ 两图", content: "梧桐叶开始落了。", location: shanghai, images: []showcaseImage{portrait(35, 1), landscape(95, 2)}},
		{key: "recent.image-three", when: ago(8 * 24 * time.Hour), note: "三图网格", content: "三张，看看 1+2 的排布。", images: []showcaseImage{landscape(250, 1), portrait(290, 2), square(10, 3)}},
		{key: "recent.media-only", when: ago(13 * 24 * time.Hour), note: "只有图片没有正文（竖长图）", images: []showcaseImage{tall(180, 1)}},
		{key: "recent.emoji", when: ago(19 * 24 * time.Hour), note: "emoji 与中英混排", content: "今天的状态：🌧️ → ☕ → 💻 → 🍜 → 😴\n\nShip small things. 小步快跑，别回头看 👀"},
		{key: "recent.edited", when: ago(26 * 24 * time.Hour), note: "发布后编辑过（edited_at）", content: "发布按钮的高度暂时先用 44px。", edit: "发布按钮的高度暂时先用 44px。\n\n（补充：后来按 X 改成了 36px。）", editAfter: 2 * 24 * time.Hour},
		{key: "year.image-nine", when: ago(34 * 24 * time.Hour), note: "九图上限，各种比例", content: "九宫格上限测试。", images: []showcaseImage{
			landscape(0, 1), portrait(40, 2), square(80, 3), panorama(120, 4), tall(160, 5), landscape(200, 6), square(240, 7), portrait(280, 8), tiny(320, 9),
		}},
		{key: "year.image-eight", when: ago(36 * 24 * time.Hour), note: "八图：首图占 2×2，其余七张小格", content: "八张：首图是横幅，占左上四格。", images: []showcaseImage{
			landscape(330, 1), square(10, 2), portrait(50, 3), landscape(90, 4), panorama(130, 5), tall(170, 6), square(210, 7), landscape(250, 8),
		}},
		{key: "year.image-seven", when: ago(37 * 24 * time.Hour), note: "七图：首图占 2×2，其余六张小格", content: "七张：首图是竖图，看它在正方形大格里怎么裁。", images: []showcaseImage{
			portrait(300, 1), landscape(340, 2), square(20, 3), panorama(60, 4), landscape(100, 5), tall(140, 6), square(180, 7),
		}},
		{key: "year.image-six", when: ago(38 * 24 * time.Hour), note: "六图：3×2 等格，没有大图", content: "六张等格，第一张是全景。", images: []showcaseImage{
			panorama(270, 1), portrait(310, 2), landscape(350, 3), square(30, 4), tall(70, 5), landscape(110, 6),
		}},
		{key: "year.image-five", when: ago(39 * 24 * time.Hour), note: "五图：首图占 2×2，其余四张小格", content: "五张：首图是正方形，正好填满大格。", images: []showcaseImage{
			square(240, 1), landscape(280, 2), portrait(320, 3), panorama(0, 4), tiny(40, 5),
		}},
		{key: "year.long-cjk-line", when: ago(41 * 24 * time.Hour), note: "超长无空格中文（换行与溢出）", content: strings.Repeat("这是一行没有任何标点也没有空格的中文用来看看容器会不会被撑破以及断行是不是按字断的", 3)},
		{key: "year.long-url", when: ago(55 * 24 * time.Hour), note: "超长 URL（自动链接与溢出）", content: "留个链接：https://example.com/very/long/path/that/keeps/going/and/going/until/it/really/has/to/wrap?utm_source=moment&utm_medium=timeline&utm_campaign=showcase-fixture-with-a-long-query-string&session=0123456789abcdef0123456789abcdef"},
		{key: "year.quote-of-hidden", when: ago(60 * 24 * time.Hour), note: "引用一条仅自己可见的帖子（访客看到「不可用」）", content: "引用一下之前那条私密记录，看看别人看到的是什么。", quotes: "year.hidden-old"},
		{key: "year.hidden-old", when: ago(70 * 24 * time.Hour), note: "仅自己可见 + 图片（图片也随之私有）", visibility: domain.VisibilityOnlyMe, content: "私密：新家的户型草图，先别发。", images: []showcaseImage{landscape(300, 1)}},
		{key: "year.link-plain", when: ago(88 * 24 * time.Hour), note: "链接卡片：无缩略图、无图标", content: "一个只有文字的卡片长什么样。",
			link: &showcaseLink{card: domain.LinkCard{
				SourceURL: "https://plain.example.org/posts/on-restraint", CanonicalURL: "https://plain.example.org/posts/on-restraint",
				Provider: domain.LinkProviderGeneric, Kind: domain.LinkKindLink,
				Title: "On Restraint", Description: "Most interfaces would be better with one fewer thing in them.", SiteName: "plain.example.org",
			}}},
		{key: "year.link-bilibili", when: ago(100 * 24 * time.Hour), note: "链接卡片：Bilibili 视频", content: "这期讲 SQLite WAL 的，讲得很清楚。",
			link: &showcaseLink{thumbnail: &biliThumb, favicon: &biliIcon, card: domain.LinkCard{
				SourceURL: "https://www.bilibili.com/video/BV1xx411c7mD", CanonicalURL: "https://www.bilibili.com/video/BV1xx411c7mD",
				Provider: domain.LinkProviderBilibili, Kind: domain.LinkKindVideo, EmbedURL: "https://player.bilibili.com/player.html?bvid=BV1xx411c7mD",
				Title: "【硬核】SQLite 的 WAL 到底是怎么工作的", Description: "从一次写入的完整路径讲起。", SiteName: "哔哩哔哩", AuthorName: "数据库小课堂",
				DurationSeconds: duration(1387), ViewCount: count(52_311), LikeCount: count(3_904), CommentCount: count(288),
			}}},
		{key: "year.photo-set", when: ago(130 * 24 * time.Hour), note: "被引用的图文帖（两图）", content: "年初的两张。", images: []showcaseImage{square(45, 1), landscape(75, 2)}},
		{key: "year.reposted-old", when: ago(160 * 24 * time.Hour), note: "半年前的帖子，几小时前被转发（时间流顶部出现「已转发」）", content: "写给半年后的自己：那个 bug 最后是时区。", repostedAt: reposted(ago(7 * time.Hour))},
		{key: "year.comments-many", when: ago(200 * 24 * time.Hour), note: "26 条根评论（分页）+ 一条根下 12 条回复 + 置顶评论", content: "问一句：大家用什么键盘？", comments: showcaseManyComments()},
		{key: "year.pinned", when: ago(240 * 24 * time.Hour), note: "站点置顶的帖子（个人主页顶部）", content: "关于这个站：我在这里记录日常，代码、照片和一些没想清楚的念头。", pinned: true, images: []showcaseImage{landscape(140, 1)},
			comments: []showcaseComment{
				{author: "lena", after: 3 * time.Hour, content: "Nice to have a place that is yours.", root: -1, replyTo: -1, pinned: true},
				root("aming", 5*time.Hour, "支持自建。"),
			}},
		{key: "year.quote-of-deleted", when: ago(280 * 24 * time.Hour), note: "引用的帖子已被删除（墓碑）", content: "引用一条后来被删掉的帖子。", quotes: "year.deleted-target"},
		{key: "year.deleted-target", when: ago(300 * 24 * time.Hour), note: "会被删除的帖子（删除后它的图片成为未引用媒体）", content: "这条稍后会被删掉，用来看引用的墓碑。", images: []showcaseImage{tiny(260, 1)}, deleted: true},
		{key: "year.code", when: ago(320 * 24 * time.Hour), note: "代码块（Go 与 JavaScript）", content: showcaseCodeMarkdown},
		{key: "calendar.new-year-eve", when: on(-1, time.December, 31, 23, 40), note: "去年最后一条（本地 23:40）", content: "跨年前最后一条。"},
		{key: "calendar.new-year", when: on(0, time.January, 1, 0, 20), note: "本地 1 月 1 日 0:20，UTC 仍是去年（归档按站点时区归位）", content: "新年第一条。本地已经是 1 月 1 日，UTC 还在去年。"},
		{key: "calendar.leap-day", when: leapDay(9, 0), note: "闰日 2 月 29 日", content: "四年一次的日子，记一笔。"},
		{key: "calendar.busy-day-1", when: on(-1, time.October, 3, 9, 10), note: "同一天三条（日历角标 3）之一", content: "假期第三天，早上先去了菜场。"},
		{key: "calendar.busy-day-2", when: on(-1, time.October, 3, 14, 30), note: "同一天三条之二", content: "下午在咖啡馆改稿。", comments: []showcaseComment{root("xiaobei", 50*time.Minute, "哪家？")}},
		{key: "calendar.busy-day-3", when: on(-1, time.October, 3, 21, 45), note: "同一天三条之三", content: "晚上散步回来，脚步数破了纪录。"},
		{key: "calendar.busy-month-a", when: on(-1, time.October, 1, 10, 0), note: "繁忙月份（去年十月）", content: "十月第一天。"},
		{key: "calendar.busy-month-b", when: on(-1, time.October, 18, 18, 0), note: "繁忙月份（去年十月）", content: "秋天正式到了。"},
		{key: "calendar.busy-month-c", when: on(-1, time.October, 25, 8, 30), note: "繁忙月份（去年十月）+ 两图", content: "早起，拍到两张。", images: []showcaseImage{landscape(215, 1), portrait(235, 2)}},
		{key: "calendar.y2-mar", when: on(-2, time.March, 8, 12, 0), note: "两年前", content: "两年前的三月。"},
		{key: "calendar.y2-jun", when: on(-2, time.June, 21, 17, 20), note: "两年前 + 图", content: "夏至，白天最长的一天。", images: []showcaseImage{panorama(55, 1)}},
		{key: "calendar.y2-sep", when: on(-2, time.September, 9, 9, 9), note: "两年前", content: "九月九日九点九分。"},
		{key: "calendar.y2-nov", when: on(-2, time.November, 11, 23, 59), note: "两年前，本地 23:59", content: "差一分钟到明天。"},
		{key: "calendar.y3-feb", when: on(-3, time.February, 14, 20, 0), note: "三年前", content: "三年前的今天在下雨。"},
		{key: "calendar.y3-jul", when: on(-3, time.July, 7, 15, 0), note: "三年前 + 手动地点", content: "东京的小巷，热得像蒸笼。", location: tokyo},
		{key: "calendar.y3-dec", when: on(-3, time.December, 24, 22, 0), note: "三年前", content: "平安夜，路上没什么人。"},
		{key: "calendar.y4-apr", when: on(-4, time.April, 5, 8, 0), note: "四年前", content: "清明，回了趟老家。"},
		{key: "calendar.y4-aug", when: on(-4, time.August, 8, 8, 8), note: "四年前 + 阳台", content: "阳台上的番茄红了。", location: balcony},
		{key: "calendar.y5-jan", when: on(-5, time.January, 1, 0, 0), note: "五年前，本地零点整", content: "零点整的第一条。"},
		{key: "calendar.y5-dec", when: on(-5, time.December, 25, 19, 30), note: "五年前 + 大图（1800×1200）", content: "圣诞的灯。", images: []showcaseImage{large(5, 1)}},
		{key: "calendar.y6-may", when: on(-6, time.May, 20, 13, 14), note: "六年前", content: "520，没什么特别的，记一下。"},
		{key: "calendar.first", when: on(-7, time.March, 3, 10, 30), note: "最早的一条（七年前），几天前被转发", content: "第一条 Moment。先写下来，再想别的。", repostedAt: reposted(ago(5 * 24 * time.Hour))},
	}
}

// showcaseManyComments fills one post past both comment page sizes: more
// roots than one page holds and, under the first root, more replies than one
// reply page holds, with answers that name who they answer.
func showcaseManyComments() []showcaseComment {
	authors := []string{"aming", "lena", "xiaobei", "owner"}
	comments := make([]showcaseComment, 0, 38)
	keyboards := []string{
		"HHKB，用了六年了。", "Keychron K3，便宜好用。", "笔记本自带的，习惯了。", "NuPhy Air75，出差带着。", "MX Keys，图个安静。",
		"Realforce，静电容一试回不去。", "GMMK Pro 换了轴。", "Apple 妙控，办公室不方便敲太响。", "IQUNIX F97，好看。", "Leopold FC660，小巧。",
		"自己焊的 60%。", "Varmilo 的樱花配色。", "Filco 87，十年老键盘。", "Logitech G Pro X。", "MelGeek Mojo68。",
		"Niz Plum 82。", "Ducky One 3。", "Keydous NJ80。", "Lofree 小翘。", "Akko 3068B。",
		"Royal Kludge RK61。", "Nuphy Halo75。", "Wooting 60HE。", "Cherry MX Board 3.0S。", "Epomaker TH80。", "还在用会员制的旧 ThinkPad 键盘。",
	}
	for index, content := range keyboards {
		comment := root(authors[index%len(authors)], time.Duration(index+1)*37*time.Minute, content)
		comment.pinned = index == 5
		comments = append(comments, comment)
	}
	replies := []string{
		"六年！按键还清脆吗？", "清脆，就是空格键有点油。", "换个键帽就好。", "键帽早换过一轮了。", "同 HHKB，Type-S 静音版。",
		"Type-S 的手感和普通版差多少？", "差不少，静音版软一点。", "我也想入一把。", "等打折。", "从来不打折 😂", "所以我买的是二手。", "二手也不便宜。",
	}
	firstReply := len(comments)
	for index, content := range replies {
		// Each reply answers the one before it, and every fourth answers
		// the root again, so the thread shows both kinds of 「回复 @」 line.
		target := firstReply + index - 1
		switch {
		case index == 0:
			target = -1
		case index%4 == 0:
			target = 0
		}
		comments = append(comments, answer(authors[(index+1)%len(authors)], 20*time.Hour+time.Duration(index+1)*11*time.Minute, 0, target, content))
	}
	return comments
}

const showcaseLongMarkdown = `# 把 composer 的三个小问题一次讲清楚

这周在 composer 上花的时间比预想的多，值得把结论写下来。

## 一、按下不动焦点

在 Safari 里按下 ` + "`<button>`" + `，之前聚焦的 textarea 会 blur 且 ` + "`relatedTarget`" + ` 为 null。任何「焦点离开就收起」的逻辑都会把按钮从指针底下抽走。

解法只有一行：

- composer 里任何不是输入框、链接、可复制提示的 ` + "`mousedown`" + ` 一律 ` + "`preventDefault()`" + `
- 焦点根本不动，click 照发，` + "`:active`" + ` 照有

> 把「按下中的指针也算在里面」当补丁是错的——空白处按下会先展开、松手再收起，连点就抽搐。

## 二、动内部高度的动画会和外框打架

1. 外框靠 ResizeObserver 量高度，再 ` + "`transition: block-size`" + `
2. 内容自己在做 240ms 过渡时，外框每帧重定目标
3. 结果是外框落后内容最多 30px、晚 100ms 收尾

折叠进行时给外框打一个标记让它逐帧贴住内容就好。

## 三、切窗口不该收起

窗口失焦也会 blur，区别只在 ` + "`document.activeElement`" + ` 还留在原元素上。判断要放在下一个 tick。

相关阅读：[MDN 上关于焦点事件的说明](https://developer.mozilla.org/docs/Web/API/Element/blur_event)，以及 *Inclusive Components* 里讲 disclosure 的那章。

---

**结论**：焦点是唯一的信号，指针只是噪音。`

const showcaseCodeMarkdown = "两段代码，看看高亮和横向滚动。\n\n```go\nfunc (viewer Viewer) CanRead(visibility domain.Visibility) bool {\n\tswitch visibility {\n\tcase domain.VisibilityEveryone:\n\t\treturn true\n\tcase domain.VisibilityOnlyMe:\n\t\treturn viewer.owner\n\tdefault:\n\t\treturn false\n\t}\n}\n```\n\n```js\nconst expanded =\n  focusWithin || audienceOpen || content.trim().length > 0;\nsection.addEventListener(\"mousedown\", (event) => {\n  if (!isTypedInto(event.target)) event.preventDefault(); // keep the caret where it is\n});\n```\n\n行内代码：`grid-template-rows: 0fr` 到 `1fr` 是可以过渡的。"

const showcaseLongComment = "认真读完了。有几点补充：\n\n第一，preventDefault 在 mousedown 上的副作用是拖选文字也一起没了，所以你把提示和链接排除掉是对的，但引用卡片里的文字就选不了了，这一点可以接受。\n\n第二，外框追内容那段，我之前在评论区的折叠上也遇到过一模一样的现象，当时的解法更笨——直接把外框的过渡关了，结果加图的时候就跳。你这个「折叠期间打标记」的思路更好。\n\n第三，切窗口收起的问题在 Chrome 和 Safari 上表现一致吗？我记得 Safari 在切回来的时候不一定会把焦点还给 textarea，如果不还的话 activeElement 还是它但没有 focus 事件，这时候 UI 是展开的、光标却不在里面。\n\n以上，感谢分享。"

func (builder *fixtureBuilder) buildShowcase(ctx context.Context) (Counts, error) {
	if err := builder.verify(ctx); err != nil {
		return Counts{}, err
	}
	anchor := builder.anchor
	zone := builder.zone
	manifest := &ShowcaseManifest{AnchoredAt: anchor.Format(time.RFC3339), Timezone: zone.String(), Owner: showcaseOwnerUsername}
	actual := Counts{}
	pictureIndex := 0
	var zeroID domain.ID
	ingest := func(ingestContext context.Context, spec showcaseImage) (domain.ID, error) {
		if err := builder.verify(ingestContext); err != nil {
			return zeroID, err
		}
		contents, err := showcasePNG(spec, builder.seed, pictureIndex)
		if err != nil {
			return zeroID, err
		}
		value, _, err := builder.media.Ingest(ingestContext, spec.fileName(pictureIndex), bytes.NewReader(contents))
		if err != nil {
			return zeroID, fmt.Errorf("ingest %s: %w", spec.fileName(pictureIndex), err)
		}
		pictureIndex++
		actual.Media++
		actual.MediaBytes += int64(len(contents))
		builder.emit("media", actual.Media, builder.recipe.Counts.Media)
		return value.ID, nil
	}
	usagesFor := func(images []showcaseImage, alt func(int) *string) ([]domain.EntryMediaUsage, error) {
		usages := make([]domain.EntryMediaUsage, 0, len(images))
		for index, spec := range images {
			id, err := ingest(ctx, spec)
			if err != nil {
				return nil, err
			}
			usage := domain.EntryMediaUsage{MediaID: id, Position: index}
			if alt != nil {
				usage.AltText = alt(index)
			} else {
				usage.AltText = text(fmt.Sprintf("示例图片 %d，%d×%d", spec.Label, spec.Width, spec.Height))
			}
			usages = append(usages, usage)
		}
		return usages, nil
	}

	// The site first: identity, pictures, policy.
	avatar := portrait(210, 0)
	avatar.Name = "avatar-owner.png"
	cover := panorama(195, 0)
	cover.Name = "cover-owner.png"
	builder.clock.now = anchor.AddDate(-7, 0, 0)
	avatarID, err := ingest(ctx, avatar)
	if err != nil {
		return Counts{}, err
	}
	coverID, err := ingest(ctx, cover)
	if err != nil {
		return Counts{}, err
	}
	timezone := zone.String()
	commentsEnabled := true
	registration := string(domain.RegistrationOpen)
	avatarValue, coverValue := avatarID.String(), coverID.String()
	if _, err := builder.accounts.UpdateProfile(ctx, builder.actor.ID, app.ProfileUpdate{
		Username: text(showcaseOwnerUsername), DisplayName: text(showcaseOwnerDisplayName),
		Bio: text("在上海写代码、拍照、记录。偶尔去别的城市待几天。\nhttps://xiaoman.example"), AvatarMediaID: &app.NullableString{Value: &avatarValue}, CoverMediaID: &app.NullableString{Value: &coverValue},
	}); err != nil {
		return Counts{}, err
	}
	if _, err := builder.site.Update(ctx, app.SiteUpdate{Title: text("小满的 Moment"), Timezone: &timezone, CommentsEnabled: &commentsEnabled, RegistrationMode: &registration}); err != nil {
		return Counts{}, err
	}

	settings, err := builder.site.Get(ctx)
	if err != nil {
		return Counts{}, err
	}
	authors := map[string]domain.User{"owner": {
		ID: settings.ProfileUserID, Username: settings.Username, DisplayName: settings.DisplayName,
		Role: domain.UserRoleAdmin, Status: domain.AccountActive,
	}}

	// Members join in the order they joined. Registration is open while the
	// active ones arrive and switches to approval before the one still
	// waiting, so the review page has someone on it.
	members := showcaseMembers()
	sort.SliceStable(members, func(i, j int) bool {
		return members[i].joined.at(anchor, zone).Before(members[j].joined.at(anchor, zone))
	})
	for _, member := range members {
		if err := builder.verify(ctx); err != nil {
			return Counts{}, err
		}
		joined := member.joined.at(anchor, zone)
		builder.clock.now = joined
		mode := domain.RegistrationOpen
		if member.status == domain.AccountPending {
			mode = domain.RegistrationApproval
		}
		modeValue := string(mode)
		if _, err := builder.site.Update(ctx, app.SiteUpdate{RegistrationMode: &modeValue}); err != nil {
			return Counts{}, err
		}
		outcome, err := builder.accounts.SignIn(ctx, app.ExternalIdentity{
			Provider: member.provider, ProviderUserID: member.providerID, Email: member.email, EmailVerified: member.email != "",
			DisplayName: member.displayName,
		})
		if err != nil {
			return Counts{}, fmt.Errorf("sign in %s: %w", member.key, err)
		}
		profile := app.ProfileUpdate{Username: text(member.username)}
		if member.bio != "" {
			profile.Bio = text(member.bio)
		}
		if member.avatar != nil {
			id, err := ingest(app.WithContentActor(ctx, outcome.User), *member.avatar)
			if err != nil {
				return Counts{}, err
			}
			value := id.String()
			profile.AvatarMediaID = &app.NullableString{Value: &value}
		}
		builder.clock.now = joined.Add(time.Minute)
		user, err := builder.accounts.UpdateProfile(ctx, outcome.User.ID, profile)
		if err != nil {
			return Counts{}, fmt.Errorf("profile %s: %w", member.key, err)
		}
		if member.status == domain.AccountSuspended {
			builder.clock.now = joined.Add(2 * time.Hour)
			user, err = builder.accounts.SetStatus(ctx, user.ID, domain.AccountSuspended)
			if err != nil {
				return Counts{}, err
			}
		}
		authors[member.key] = user
		manifest.Cases = append(manifest.Cases, ShowcaseCase{Key: "member." + member.key, ID: user.ID, Path: "/" + user.Username, Note: member.note})
	}
	approval := string(domain.RegistrationApproval)
	builder.clock.now = anchor
	if _, err := builder.site.Update(ctx, app.SiteUpdate{RegistrationMode: &approval}); err != nil {
		return Counts{}, err
	}

	// Posts in the order they were written, so a quote always finds its
	// target and every identifier carries the moment it names.
	posts := showcasePosts()
	sort.SliceStable(posts, func(i, j int) bool {
		return posts[i].when.at(anchor, zone).Before(posts[j].when.at(anchor, zone))
	})
	ids := make(map[string]domain.ID, len(posts))
	var pinned *domain.ID
	type later struct {
		at  time.Time
		run func() error
	}
	var deferred []later
	for _, post := range posts {
		if err := builder.verify(ctx); err != nil {
			return Counts{}, err
		}
		at := post.when.at(anchor, zone)
		usages, err := usagesFor(post.images, post.alt)
		if err != nil {
			return Counts{}, err
		}
		input := app.CreatePostInput{
			ContentMD: post.content, Visibility: post.visibility, Media: usages, Location: post.location,
			IdempotencyKey: fmt.Sprintf("dev-%s-%d-%s", builder.recipe.Name, builder.seed, post.key),
		}
		if post.link != nil {
			card := post.link.card
			card.Position = 0
			card.CapturedAt = domain.TimeToMillis(at)
			if post.link.thumbnail != nil {
				id, err := ingest(ctx, *post.link.thumbnail)
				if err != nil {
					return Counts{}, err
				}
				card.ThumbnailMediaID = id.String()
			}
			if post.link.favicon != nil {
				id, err := ingest(ctx, *post.link.favicon)
				if err != nil {
					return Counts{}, err
				}
				card.FaviconMediaID = id.String()
			}
			input.LinkCards = []domain.LinkCard{card}
		}
		if post.quotes != "" {
			target, found := ids[post.quotes]
			if !found {
				return Counts{}, fmt.Errorf("showcase post %s quotes %s, which is not written yet", post.key, post.quotes)
			}
			input.QuotedPostID = &target
		}
		builder.clock.now = at
		created, err := builder.posts.Create(ctx, input)
		if err != nil {
			return Counts{}, fmt.Errorf("create %s: %w", post.key, err)
		}
		id := created.Post.Entry.ID
		ids[post.key] = id
		actual.Entries++
		builder.emit("entry", actual.Entries, builder.recipe.Counts.Entries)
		manifest.Cases = append(manifest.Cases, ShowcaseCase{Key: "post." + post.key, ID: id.String(), Path: "/m/" + id.String(), Note: post.note})

		written, err := builder.createShowcaseComments(ctx, id, at, post.comments, authors, ingest, &actual)
		if err != nil {
			return Counts{}, fmt.Errorf("comments on %s: %w", post.key, err)
		}
		actual.Comments += written
		if post.edit != "" {
			editAt := at.Add(post.editAfter)
			deferred = append(deferred, later{at: editAt, run: func() error {
				builder.clock.now = editAt
				_, err := builder.posts.Update(ctx, app.UpdatePostInput{ID: id, ContentMD: post.edit})
				return err
			}})
		}
		if post.repostedAt != nil {
			repostAt := post.repostedAt.at(anchor, zone)
			deferred = append(deferred, later{at: repostAt, run: func() error {
				builder.clock.now = repostAt
				_, err := builder.posts.SetReposted(ctx, id, true)
				return err
			}})
		}
		if post.pinned {
			pin := id
			pinned = &pin
		}
		if post.deleted {
			caseIndex := len(manifest.Cases) - 1
			deferred = append(deferred, later{at: anchor.Add(-time.Hour), run: func() error {
				builder.clock.now = anchor.Add(-time.Hour)
				if _, err := builder.posts.Delete(ctx, id); err != nil {
					return err
				}
				actual.Entries--
				// The case stays listed under its key so the tombstone it
				// left behind can be traced to it, but it has no page.
				manifest.Cases[caseIndex].Path = ""
				manifest.Cases[caseIndex].Note = "已删除：" + manifest.Cases[caseIndex].Note
				return nil
			}})
		}
	}
	// Edits, reposts, and the one deletion happen after the posts they touch
	// and in the order they happened, so every updated_at is later than
	// what it updates.
	sort.SliceStable(deferred, func(i, j int) bool { return deferred[i].at.Before(deferred[j].at) })
	for _, action := range deferred {
		if err := builder.verify(ctx); err != nil {
			return Counts{}, err
		}
		if err := action.run(); err != nil {
			return Counts{}, err
		}
	}
	if pinned != nil {
		builder.clock.now = anchor.Add(-30 * 24 * time.Hour)
		if _, err := builder.site.Pin(ctx, *pinned); err != nil {
			return Counts{}, err
		}
	}
	builder.clock.now = anchor
	if actual != builder.recipe.Counts {
		return Counts{}, fmt.Errorf("fixture counts drifted: got=%+v want=%+v", actual, builder.recipe.Counts)
	}
	builder.manifest = manifest
	return actual, nil
}

func (builder *fixtureBuilder) createShowcaseComments(ctx context.Context, entryID domain.ID, postedAt time.Time, comments []showcaseComment, authors map[string]domain.User, ingest func(context.Context, showcaseImage) (domain.ID, error), actual *Counts) (int64, error) {
	created := make([]domain.ID, 0, len(comments))
	var written int64
	for index, comment := range comments {
		if err := builder.verify(ctx); err != nil {
			return written, err
		}
		author, found := authors[comment.author]
		if !found {
			return written, fmt.Errorf("comment %d names unknown author %q", index, comment.author)
		}
		at := postedAt.Add(comment.after)
		if !at.Before(builder.anchor) {
			at = builder.anchor.Add(-time.Duration(len(comments)-index) * time.Minute)
		}
		input := app.CreateCommentInput{
			EntryID: entryID, Author: author, ContentMD: comment.content, Location: comment.location,
			IdempotencyKey: fmt.Sprintf("dev-%s-%d-%s-comment-%d", builder.recipe.Name, builder.seed, entryID, index),
		}
		if comment.root >= 0 {
			if comment.root >= len(created) {
				return written, fmt.Errorf("comment %d replies to root %d, which is not written yet", index, comment.root)
			}
			parent := created[comment.root]
			input.ParentID = &parent
			if comment.replyTo >= 0 {
				if comment.replyTo >= len(created) {
					return written, fmt.Errorf("comment %d answers comment %d, which is not written yet", index, comment.replyTo)
				}
				target := created[comment.replyTo]
				input.ReplyToCommentID = &target
			}
		}
		for position, spec := range comment.images {
			id, err := ingest(app.WithContentActor(ctx, author), spec)
			if err != nil {
				return written, err
			}
			input.Media = append(input.Media, domain.EntryMediaUsage{MediaID: id, Position: position, AltText: text(fmt.Sprintf("评论配图 %d", spec.Label))})
		}
		builder.clock.now = at
		result, err := builder.comments.Create(ctx, input)
		if err != nil {
			return written, fmt.Errorf("comment %d: %w", index, err)
		}
		created = append(created, result.Comment.Comment.ID)
		written++
		builder.emit("comment", actual.Comments+written, builder.recipe.Counts.Comments)
		if comment.pinned {
			builder.clock.now = at.Add(time.Minute)
			if _, err := builder.comments.Pin(ctx, entryID, result.Comment.Comment.ID); err != nil {
				return written, err
			}
		}
	}
	return written, nil
}

// showcaseCounts is derived from the plan so the catalog can never disagree
// with what the builder writes: every picture is budgeted to an exact size,
// and the one deleted post is not counted.
func showcaseCounts() Counts {
	counts := Counts{}
	picture := func(spec showcaseImage) {
		counts.Media++
		counts.MediaBytes += spec.Bytes
	}
	picture(portrait(0, 0))
	picture(panorama(0, 0))
	for _, member := range showcaseMembers() {
		if member.avatar != nil {
			picture(*member.avatar)
		}
	}
	for _, post := range showcasePosts() {
		if !post.deleted {
			counts.Entries++
		}
		for _, spec := range post.images {
			picture(spec)
		}
		if post.link != nil {
			if post.link.thumbnail != nil {
				picture(*post.link.thumbnail)
			}
			if post.link.favicon != nil {
				picture(*post.link.favicon)
			}
		}
		for _, comment := range post.comments {
			counts.Comments++
			for _, spec := range comment.images {
				picture(spec)
			}
		}
	}
	return counts
}

// showcaseZone is the site's time zone. The showcase is a Shanghai site, and
// its calendar cases only mean something when the archive groups days in
// that zone; a host without zone data gets UTC and the same posts, only
// grouped differently.
func showcaseZone() (*time.Location, string) {
	if zone, err := time.LoadLocation("Asia/Shanghai"); err == nil {
		return zone, "Asia/Shanghai"
	}
	return time.UTC, "UTC"
}

func newShowcaseBuilderWithinLocks(target *Target, runtimeState *runtimefs.Runtime, database *sqlitestore.Database, authority *runtimefs.DevelopmentAuthority, held *runtimefs.LeaseSet, staged *generationGuard, recipe Scenario, seed int64, anchor time.Time, progress func(Progress), postRepository *sqlitestore.PostRepository, commentRepository *sqlitestore.CommentRepository, siteRepository *sqlitestore.SiteRepository) (*fixtureBuilder, error) {
	accountRepository, err := sqlitestore.NewDevelopmentAccountRepository(database, runtimeState.Root, held)
	if err != nil {
		return nil, err
	}
	if anchor.IsZero() {
		anchor = time.Now()
	}
	anchor = anchor.UTC().Truncate(time.Minute)
	zone, _ := showcaseZone()
	clock := &settableClock{now: anchor}
	_, postIDs := deterministicEntitySources(recipe.Name, seed, "post", func(int64) time.Time { return anchor })
	_, commentIDs := deterministicEntitySources(recipe.Name, seed, "comment", func(int64) time.Time { return anchor })
	posts, err := app.NewPostService(postRepository, app.WithPostClock(clock.Now), app.WithPostIDGenerator(app.PostIDGenerator(postIDs)))
	if err != nil {
		return nil, err
	}
	comments, err := app.NewCommentService(commentRepository, app.WithCommentClock(clock.Now), app.WithCommentIDGenerator(app.CommentIDGenerator(commentIDs)))
	if err != nil {
		return nil, err
	}
	site, err := app.NewSiteService(siteRepository, app.SiteOptions{AllowDevelopmentLocalHTTP: true})
	if err != nil {
		return nil, err
	}
	accounts, err := app.NewAccountService(accountRepository, app.WithAccountClock(clock.Now), app.WithAccountIDGenerator(showcaseAccountIDs(seed)))
	if err != nil {
		return nil, err
	}
	mediaStore, err := media.NewDevelopmentStore(runtimeState, database, media.Options{
		Clock: indexedClock(anchor.AddDate(-8, 0, 0), time.Millisecond),
		Entropy: &hashStream{
			key: []byte(fmt.Sprintf("%s|%s|%d|media", RecipeVersion, recipe.Name, seed)),
		},
	}, authority, held)
	if err != nil {
		return nil, err
	}
	actor, err := accounts.ProfileAccount(context.Background())
	if err != nil {
		return nil, err
	}
	return &fixtureBuilder{
		target: target, runtime: runtimeState, database: database, recipe: recipe,
		seed: seed, base: anchor, posts: posts, comments: comments, site: site,
		media: mediaStore, staged: staged, progress: progress,
		accounts: accounts, clock: clock, anchor: anchor, zone: zone, actor: actor,
	}, nil
}

func showcaseAccountIDs(seed int64) func() (string, error) {
	var index int64
	return func() (string, error) {
		key := fmt.Sprintf("%s|%s|%d|account|%d", RecipeVersion, ShowcaseScenarioName, seed, index)
		index++
		digest := sha256.Sum256([]byte(key))
		return hex.EncodeToString(digest[:16]), nil
	}
}

// settableClock is the one clock every showcase service reads. The builder
// sets it to the instant each write is meant to have happened at, so posts
// spread over years, comments follow their posts, and edits and reposts land
// after the things they touch.
type settableClock struct {
	now time.Time
}

func (clock *settableClock) Now() time.Time { return clock.now }
