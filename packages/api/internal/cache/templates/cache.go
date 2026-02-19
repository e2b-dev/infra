package templatecache

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	sqlcdb "github.com/e2b-dev/infra/packages/db/client"
	"github.com/e2b-dev/infra/packages/db/pkg/dberrors"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/cache"
	"github.com/e2b-dev/infra/packages/shared/pkg/clusters"
	"github.com/e2b-dev/infra/packages/shared/pkg/id"
	sharedUtils "github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

const (
	templateCacheTTL             = 5 * time.Minute
	templateCacheRefreshInterval = 1 * time.Minute
	templateCacheTimeout         = 2 * time.Second

	templateCacheKeyPrefix = "template:info"
)

var tracer = otel.Tracer("github.com/e2b-dev/infra/packages/api/internal/cache/templates")

func buildCacheKey(templateID, tag string) string {
	// Wrap templateID in {} so it becomes a Redis hash tag — all keys for the
	// same template land on the same hash slot in Redis Cluster, enabling
	// pipelined prefix deletion in InvalidateAllTags.
	return fmt.Sprintf("{%s}:%s", templateID, tag)
}

// TemplateInfo holds cached template with build information
type TemplateInfo struct {
	Template  *api.Template     `json:"template"`
	TeamID    uuid.UUID         `json:"team_id"`
	ClusterID uuid.UUID         `json:"cluster_id"`
	Build     *queries.EnvBuild `json:"build"`
	Tag       string            `json:"tag"`
}

// TemplateCache caches template+build by templateID:tag.
// This is a simple lookup layer - resolution happens in AliasCache.
type TemplateCache struct {
	cache      *cache.RedisCache[*TemplateInfo]
	db         *sqlcdb.Client
	aliasCache *AliasCache
}

func NewTemplateCache(db *sqlcdb.Client, redisClient redis.UniversalClient) *TemplateCache {
	redisCache := cache.NewRedisCache[*TemplateInfo](cache.RedisConfig[*TemplateInfo]{
		TTL:             templateCacheTTL,
		RefreshInterval: templateCacheRefreshInterval,
		RedisTimeout:    templateCacheTimeout,
		RedisClient:     redisClient,
		RedisPrefix:     templateCacheKeyPrefix,

		ExtractKeyFunc: func(value *TemplateInfo) string {
			return buildCacheKey(value.Template.TemplateID, value.Tag)
		},
	})

	return &TemplateCache{
		cache:      redisCache,
		db:         db,
		aliasCache: NewAliasCache(db),
	}
}

// ResolveAlias resolves an identifier to AliasInfo (templateID, teamID, public).
// The identifier is "namespace/alias" or just "alias" (already validated by id.ParseName).
// namespaceFallback is used for bare aliases (no explicit namespace).
func (c *TemplateCache) ResolveAlias(ctx context.Context, identifier string, namespaceFallback string) (*AliasInfo, error) {
	return c.aliasCache.Resolve(ctx, identifier, namespaceFallback)
}

// GetByID looks up template info by direct template ID only (no alias resolution).
func (c *TemplateCache) GetByID(ctx context.Context, templateID string) (*AliasInfo, error) {
	return c.aliasCache.LookupByID(ctx, templateID)
}

// Get fetches a template with build by templateID and tag.
// Does NOT do alias resolution - callers should use ResolveAlias first.
// Performs access control and cluster checks.
func (c *TemplateCache) Get(ctx context.Context, templateID string, tag *string, teamID uuid.UUID, clusterID uuid.UUID) (*api.Template, *queries.EnvBuild, error) {
	ctx, span := tracer.Start(ctx, "get template")
	defer span.End()

	// Step 1: Get template with build by ID and tag
	templateInfo, err := c.getByID(ctx, templateID, tag)
	if err != nil {
		return nil, nil, err
	}

	// Step 2: Access control check
	if templateInfo.TeamID != teamID && !templateInfo.Template.Public {
		return nil, nil, fmt.Errorf("%w: team '%s' cannot access template '%s'", ErrAccessDenied, teamID, templateID)
	}

	// Step 3: Cluster check
	if templateInfo.ClusterID != clusterID {
		return nil, nil, fmt.Errorf("%w: template '%s' not in cluster '%s'", ErrClusterMismatch, templateID, clusterID)
	}

	return templateInfo.Template, templateInfo.Build, nil
}

// getByID fetches template+build by templateID and tag
func (c *TemplateCache) getByID(ctx context.Context, templateID string, tag *string) (*TemplateInfo, error) {
	tagValue := id.DefaultTag
	if tag != nil {
		tagValue = *tag
	}
	cacheKey := buildCacheKey(templateID, tagValue)

	info, err := c.cache.GetOrSet(ctx, cacheKey, c.fetchTemplateWithBuild(templateID, tag))
	if err != nil {
		return nil, err
	}

	return info, nil
}

func (c *TemplateCache) fetchTemplateWithBuild(templateID string, tag *string) func(context.Context, string) (*TemplateInfo, error) {
	return func(ctx context.Context, _ string) (*TemplateInfo, error) {
		ctx, span := tracer.Start(ctx, "fetch template with build")
		defer span.End()

		result, err := c.db.GetTemplateWithBuildByTag(ctx, queries.GetTemplateWithBuildByTagParams{
			TemplateID: templateID,
			Tag:        tag,
		})
		if err != nil {
			if dberrors.IsNotFoundError(err) {
				return nil, ErrTemplateNotFound
			}

			return nil, fmt.Errorf("fetching template with build: %w", err)
		}

		build := &result.EnvBuild
		template := result.Env
		clusterID := clusters.WithClusterFallback(template.ClusterID)

		tagValue := sharedUtils.DerefOrDefault(tag, id.DefaultTag)

		return &TemplateInfo{
			Template: &api.Template{
				TemplateID: template.ID,
				BuildID:    build.ID.String(),
				Public:     template.Public,
				Aliases:    result.Aliases,
				Names:      result.Names,
			},
			TeamID:    template.TeamID,
			ClusterID: clusterID,
			Build:     build,
			Tag:       tagValue,
		}, nil
	}
}

func (c *TemplateCache) Invalidate(ctx context.Context, templateID string, tag *string) {
	tagValue := id.DefaultTag
	if tag != nil {
		tagValue = *tag
	}
	cacheKey := buildCacheKey(templateID, tagValue)
	c.cache.Delete(ctx, cacheKey)
}

// InvalidateAllTags invalidates the cache for the given templateID across all tags
func (c *TemplateCache) InvalidateAllTags(ctx context.Context, templateID string) []string {
	pattern := buildCacheKey(templateID, "")
	keys := c.cache.DeleteByPrefix(ctx, pattern)

	c.aliasCache.InvalidateByTemplateID(templateID)

	return keys
}

// InvalidateAlias invalidates the alias cache entry
func (c *TemplateCache) InvalidateAlias(namespace *string, alias string) {
	c.aliasCache.Invalidate(namespace, alias)
}

func (c *TemplateCache) Close(ctx context.Context) error {
	return errors.Join(c.aliasCache.Close(ctx), c.cache.Close(ctx))
}
