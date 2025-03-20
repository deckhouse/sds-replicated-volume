package scheduler

import (
	"context"
	"net/http"
	"scheduler-extender/pkg/cache"
	"scheduler-extender/pkg/logger"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

type scheduler struct {
	defaultDivisor float64
	log            logger.Logger
	client         client.Client
	ctx            context.Context
	cache          *cache.Cache
	requestCount   int
}

func (s *scheduler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/filter":
		s.filter(w, r)
	case "/prioritize":
		s.prioritize(w, r)
	}
}

func NewHandler(ctx context.Context, cl client.Client, log logger.Logger, lvgCache *cache.Cache, defaultDiv float64) (http.Handler, error) {
	return &scheduler{
		defaultDivisor: defaultDiv,
		log:            log,
		client:         cl,
		ctx:            ctx,
		cache:          lvgCache,
	}, nil
}
