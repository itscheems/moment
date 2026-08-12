package devtools

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"io"
	"math"
	"strings"
	"time"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/media"
	"github.com/moment/moment/internal/runtimefs"
	sqlitestore "github.com/moment/moment/internal/storage/sqlite"
)

type fixtureBuilder struct {
	target   *Target
	runtime  *runtimefs.Runtime
	database *sqlitestore.Database
	recipe   Scenario
	seed     int64
	base     time.Time
	posts    *app.PostService
	comments *app.CommentService
	site     *app.SiteService
	media    *media.Store
	staged   *generationGuard
	progress func(Progress)
}

func newFixtureBuilderWithinLocks(target *Target, runtimeState *runtimefs.Runtime, database *sqlitestore.Database, authority *runtimefs.DevelopmentAuthority, held *runtimefs.LeaseSet, staged *generationGuard, recipe Scenario, seed int64, progress func(Progress)) (*fixtureBuilder, error) {
	postRepository, err := sqlitestore.NewDevelopmentPostRepository(database, runtimeState.Root, held)
	if err != nil {
		return nil, err
	}
	commentRepository, err := sqlitestore.NewDevelopmentCommentRepository(database, runtimeState.Root, held)
	if err != nil {
		return nil, err
	}
	siteRepository, err := sqlitestore.NewDevelopmentSiteRepository(database, runtimeState.Root, held)
	if err != nil {
		return nil, err
	}
	base := deterministicBase(recipe.Name, seed)
	postClock, postIDs := deterministicEntitySources(recipe.Name, seed, "post", postTime(recipe, base))
	commentClock, commentIDs := deterministicEntitySources(recipe.Name, seed, "comment", func(index int64) time.Time {
		return base.Add(12*time.Hour + time.Duration(index)*time.Second)
	})
	posts, err := app.NewPostService(postRepository, app.WithPostClock(app.PostClock(postClock)), app.WithPostIDGenerator(app.PostIDGenerator(postIDs)))
	if err != nil {
		return nil, err
	}
	comments, err := app.NewCommentService(commentRepository, app.WithCommentClock(app.CommentClock(commentClock)), app.WithCommentIDGenerator(app.CommentIDGenerator(commentIDs)))
	if err != nil {
		return nil, err
	}
	site, err := app.NewSiteService(siteRepository, app.SiteOptions{AllowDevelopmentLocalHTTP: true, Clock: func() time.Time { return base }})
	if err != nil {
		return nil, err
	}
	mediaClock := indexedClock(base.Add(-24*time.Hour), time.Millisecond)
	mediaStore, err := media.NewDevelopmentStore(runtimeState, database, media.Options{
		Clock: mediaClock,
		Entropy: &hashStream{
			key: []byte(fmt.Sprintf("%s|%s|%d|media", RecipeVersion, recipe.Name, seed)),
		},
	}, authority, held)
	if err != nil {
		return nil, err
	}
	return &fixtureBuilder{
		target: target, runtime: runtimeState, database: database, recipe: recipe,
		seed: seed, base: base, posts: posts, comments: comments, site: site,
		media: mediaStore, staged: staged, progress: progress,
	}, nil
}

func (builder *fixtureBuilder) build(ctx context.Context) (Counts, error) {
	if err := builder.verify(ctx); err != nil {
		return Counts{}, err
	}
	title := "Moment development"
	bio := fmt.Sprintf("%s recipe %s seed %d", builder.recipe.Name, RecipeVersion, builder.seed)
	timezone := "UTC"
	commentsEnabled := true
	if _, err := builder.site.Update(ctx, app.SiteUpdate{
		Title: &title, Bio: &bio, Timezone: &timezone, CommentsEnabled: &commentsEnabled,
	}); err != nil {
		return Counts{}, err
	}

	actual := Counts{}
	var firstMedia *domain.ID
	for entryIndex := int64(0); entryIndex < builder.recipe.Counts.Entries; entryIndex++ {
		if err := builder.verify(ctx); err != nil {
			return Counts{}, err
		}
		newMediaCount, usageCount := mediaAllocation(builder.recipe, entryIndex)
		usages := make([]domain.EntryMediaUsage, 0, usageCount)
		for localIndex := int64(0); localIndex < newMediaCount; localIndex++ {
			mediaIndex := actual.Media
			if err := builder.verify(ctx); err != nil {
				return Counts{}, err
			}
			size := allocatedBytes(builder.recipe.Counts.MediaBytes, builder.recipe.Counts.Media, mediaIndex)
			contents, err := deterministicPNG(builder.recipe.Name, builder.seed, mediaIndex, size)
			if err != nil {
				return Counts{}, err
			}
			value, _, err := builder.media.Ingest(ctx, fmt.Sprintf("fixture-%05d.png", mediaIndex), bytes.NewReader(contents))
			if err != nil {
				return Counts{}, err
			}
			if firstMedia == nil {
				id := value.ID
				firstMedia = &id
			}
			alt := altFor(builder.recipe.Name, mediaIndex)
			usages = append(usages, domain.EntryMediaUsage{MediaID: value.ID, Position: len(usages), AltText: alt})
			actual.Media++
			actual.MediaBytes += int64(len(contents))
			builder.emit("media", actual.Media, builder.recipe.Counts.Media)
		}
		for int64(len(usages)) < usageCount && len(usages) != 0 {
			original := usages[len(usages)%int(newMediaCount)]
			original.Position = len(usages)
			if len(usages)%3 == 0 {
				original.AltText = nil
			}
			usages = append(usages, original)
		}
		content := contentFor(builder.recipe.Name, builder.seed, entryIndex)
		created, err := builder.posts.Create(ctx, app.CreatePostInput{
			ContentMD: content, Media: usages,
			IdempotencyKey: fmt.Sprintf("dev-%s-%d-post-%d", builder.recipe.Name, builder.seed, entryIndex),
		})
		if err != nil {
			return Counts{}, err
		}
		actual.Entries++
		builder.emit("entry", actual.Entries, builder.recipe.Counts.Entries)
		commentsForPost := allocation(builder.recipe.Counts.Comments, builder.recipe.Counts.Entries, entryIndex)
		createdComments, err := builder.createComments(ctx, created.Post.Entry.ID, actual.Comments, commentsForPost, usages)
		if err != nil {
			return Counts{}, err
		}
		actual.Comments += createdComments
	}
	if actual != builder.recipe.Counts {
		return Counts{}, fmt.Errorf("fixture counts drifted: got=%+v want=%+v", actual, builder.recipe.Counts)
	}
	if firstMedia != nil && builder.recipe.Name == "archive-roundtrip" {
		value := firstMedia.String()
		if _, err := builder.site.Update(ctx, app.SiteUpdate{AvatarMediaID: &app.NullableString{Value: &value}}); err != nil {
			return Counts{}, err
		}
	}
	return actual, nil
}

func (builder *fixtureBuilder) createComments(ctx context.Context, entryID domain.ID, offset, count int64, postMedia []domain.EntryMediaUsage) (int64, error) {
	if count == 0 {
		return 0, nil
	}
	var root domain.ID
	for index := int64(0); index < count; index++ {
		if err := builder.verify(ctx); err != nil {
			return index, err
		}
		input := app.CreateCommentInput{
			EntryID: entryID, ContentMD: fmt.Sprintf("fixture comment %d for %s", offset+index, builder.recipe.Name),
			IdempotencyKey: fmt.Sprintf("dev-%s-%d-comment-%d", builder.recipe.Name, builder.seed, offset+index),
		}
		if builder.recipe.Name == "archive-roundtrip" && len(postMedia) != 0 {
			limit := 1
			if index%3 == 0 && len(postMedia) > 1 {
				limit = 2
			}
			input.Media = domain.CloneEntryMediaUsages(postMedia[:limit])
			for position := range input.Media {
				input.Media[position].Position = position
			}
		}
		var result app.CreateCommentResult
		var err error
		switch {
		case index == 0 || index%3 == 0:
			if index%2 == 0 {
				input.AuthorName = "Visitor"
				result, err = builder.comments.CreateGuest(ctx, input)
			} else {
				result, err = builder.comments.CreateOwner(ctx, input)
			}
			if err == nil && index == 0 {
				root = result.Comment.Comment.ID
			}
		case index%2 == 0:
			input.ParentID = &root
			input.AuthorName = "Reply visitor"
			result, err = builder.comments.CreateGuest(ctx, input)
		default:
			input.ParentID = &root
			result, err = builder.comments.CreateOwner(ctx, input)
		}
		if err != nil {
			return index, err
		}
		builder.emit("comment", offset+index+1, builder.recipe.Counts.Comments)
	}
	return count, nil
}

func (builder *fixtureBuilder) verify(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := builder.target.Verify(); err != nil {
		return err
	}
	return builder.staged.Verify(builder.target)
}

func (builder *fixtureBuilder) emit(kind string, completed, total int64) {
	if builder.progress != nil && (completed == 1 || completed == total || completed%256 == 0) {
		builder.progress(Progress{Kind: kind, Completed: completed, Total: total})
	}
}

func mediaAllocation(recipe Scenario, entryIndex int64) (newMedia, usages int64) {
	if recipe.Name == "post-with-images" {
		switch entryIndex {
		case 0:
			return 1, 1
		case 1:
			return 4, 4
		default:
			return 7, 9
		}
	}
	count := allocation(recipe.Counts.Media, recipe.Counts.Entries, entryIndex)
	return count, count
}

func allocation(total, groups, index int64) int64 {
	if groups <= 0 || total <= 0 {
		return 0
	}
	return (index+1)*total/groups - index*total/groups
}

func allocatedBytes(total, count, index int64) int64 {
	if count == 0 {
		return 0
	}
	return total/count + boolInt64(index < total%count)
}

func boolInt64(value bool) int64 {
	if value {
		return 1
	}
	return 0
}

func altFor(recipe string, mediaIndex int64) *string {
	if recipe == "post-images-missing-alt" || recipe == "archive-roundtrip" {
		switch mediaIndex % 3 {
		case 0:
			return nil
		case 1:
			value := ""
			return &value
		}
	}
	value := fmt.Sprintf("deterministic image %d", mediaIndex)
	return &value
}

func contentFor(recipe string, seed, index int64) string {
	switch recipe {
	case "post-long-markdown":
		return strings.Repeat("界", domain.MaxContentCodePoints)
	case "search-cjk", "corruption-search-drift":
		values := []string{
			"旧城下了一场雨，记住窗边的灯。", "A quiet walk through the old city.",
			"编号 20260811 与夏日回忆", "上海 夜晚 river lights", "短语搜索：桂花 乌龙", "plain numeric 314159",
		}
		return values[index%int64(len(values))]
	case "archive-years":
		return fmt.Sprintf("Archive memory %02d — year marker", index)
	case "archive-roundtrip":
		return fmt.Sprintf("Portable **Moment** %d seed %d — 迁移保持原文", index, seed)
	default:
		return fmt.Sprintf("Development Moment %04d\n\nseed=%d recipe=%s", index, seed, recipe)
	}
}

func postTime(recipe Scenario, base time.Time) func(int64) time.Time {
	return func(index int64) time.Time {
		switch recipe.Name {
		case "archive-years":
			return time.Date(2017+int(index%6), time.Month(index%12)+1, int(index%27)+1, 8, 30, 0, 0, time.UTC)
		case "archive-roundtrip":
			return time.Date(2021+int(index), time.Month(index*2+1), int(index)+1, 10, 0, 0, 0, time.UTC)
		default:
			return base.Add(-time.Duration(index) * 6 * time.Hour)
		}
	}
}

func deterministicBase(recipe string, seed int64) time.Time {
	digest := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%d|clock", RecipeVersion, recipe, seed)))
	days := binary.BigEndian.Uint32(digest[:4]) % (365 * 5)
	return time.Date(2026, time.January, 1, 12, 0, 0, 0, time.UTC).Add(-time.Duration(days) * 24 * time.Hour)
}

func deterministicEntitySources(recipe string, seed int64, namespace string, timeAt func(int64) time.Time) (func() time.Time, func(time.Time) (domain.ID, error)) {
	var clockIndex, idIndex int64
	clock := func() time.Time {
		value := timeAt(clockIndex)
		clockIndex++
		return value
	}
	ids := func(now time.Time) (domain.ID, error) {
		key := fmt.Sprintf("%s|%s|%d|%s|%d", RecipeVersion, recipe, seed, namespace, idIndex)
		idIndex++
		digest := sha256.Sum256([]byte(key))
		return domain.NewID(now, bytes.NewReader(digest[:10]))
	}
	return clock, ids
}

func indexedClock(base time.Time, step time.Duration) func() time.Time {
	var index int64
	return func() time.Time {
		value := base.Add(time.Duration(index) * step)
		index++
		return value
	}
}

type hashStream struct {
	key     []byte
	counter uint64
	buffer  []byte
}

func (stream *hashStream) Read(destination []byte) (int, error) {
	written := 0
	for written < len(destination) {
		if len(stream.buffer) == 0 {
			block := make([]byte, len(stream.key)+8)
			copy(block, stream.key)
			binary.BigEndian.PutUint64(block[len(stream.key):], stream.counter)
			stream.counter++
			digest := sha256.Sum256(block)
			stream.buffer = append(stream.buffer[:0], digest[:]...)
		}
		copied := copy(destination[written:], stream.buffer)
		stream.buffer = stream.buffer[copied:]
		written += copied
	}
	return written, nil
}

func deterministicPNG(recipe string, seed, index, size int64) ([]byte, error) {
	if size > media.DefaultMaxBytes || size > int64(math.MaxInt) {
		return nil, fmt.Errorf("fixture image size %d exceeds the media product bound", size)
	}
	canvas := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	digest := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%d|png|%d", RecipeVersion, recipe, seed, index)))
	canvas.SetNRGBA(0, 0, color.NRGBA{R: digest[0], G: digest[1], B: digest[2], A: 0xff})
	var encoded bytes.Buffer
	encoder := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := encoder.Encode(&encoded, canvas); err != nil {
		return nil, err
	}
	base := encoded.Bytes()
	const chunkOverhead = int64(12)
	if size < int64(len(base))+chunkOverhead {
		return nil, fmt.Errorf("fixture image size %d is below the deterministic PNG minimum", size)
	}
	payloadSize := size - int64(len(base)) - chunkOverhead
	result := make([]byte, 0, size)
	result = append(result, base[:len(base)-12]...)
	payload := make([]byte, payloadSize)
	stream := &hashStream{key: []byte(hex.EncodeToString(digest[:]))}
	if _, err := io.ReadFull(stream, payload); err != nil {
		return nil, err
	}
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(payloadSize))
	result = append(result, length[:]...)
	chunkType := []byte("moMe")
	result = append(result, chunkType...)
	result = append(result, payload...)
	checksum := crc32.NewIEEE()
	_, _ = checksum.Write(chunkType)
	_, _ = checksum.Write(payload)
	var sum [4]byte
	binary.BigEndian.PutUint32(sum[:], checksum.Sum32())
	result = append(result, sum[:]...)
	result = append(result, base[len(base)-12:]...)
	if int64(len(result)) != size {
		return nil, errors.New("deterministic PNG length drifted")
	}
	return result, nil
}
