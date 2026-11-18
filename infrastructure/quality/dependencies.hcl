// Only build prerequisites are exported. Gate execution stays in Make.
variable "CACHE_NAMESPACE" {
  default = "verify-linux-amd64-v1"
}

function "cache_from" {
  params = [name]
  result = ["type=gha,version=2,scope=${CACHE_NAMESPACE}-${name}"]
}

function "cache_to" {
  params = [name]
  result = ["type=gha,version=2,scope=${CACHE_NAMESPACE}-${name},mode=max,timeout=90s,ignore-error=true"]
}

group "default" {
  targets = ["go-modules", "web-dependencies", "npm-tools", "api-modules", "postgres-distribution"]
}

// Importing a remote reference alone need not retain its snapshot for later builds.
// Run only the readiness stage afresh while its cache provider is available.
target "_cache-only" {
  output = ["type=cacheonly"]
}

target "go-modules" {
  inherits = ["_cache-only"]
  context = "."
  dockerfile = "infrastructure/quality/Dockerfile"
  target = "go-cache-ready"
  no-cache-filter = ["go-cache-ready"]
  cache-from = cache_from("go-modules")
  cache-to = cache_to("go-modules")
}

target "web-dependencies" {
  inherits = ["_cache-only"]
  context = "."
  dockerfile = "apps/web/Dockerfile"
  target = "cache-ready"
  no-cache-filter = ["cache-ready"]
  cache-from = cache_from("web-dependencies")
  cache-to = cache_to("web-dependencies")
}

target "npm-tools" {
  inherits = ["_cache-only"]
  context = "."
  dockerfile = "infrastructure/quality/Dockerfile"
  target = "npm-cache-ready"
  no-cache-filter = ["npm-cache-ready"]
  cache-from = cache_from("npm-tools")
  cache-to = cache_to("npm-tools")
}

target "api-modules" {
  inherits = ["_cache-only"]
  context = "apps/api"
  dockerfile = "Dockerfile"
  target = "cache-ready"
  no-cache-filter = ["cache-ready"]
  cache-from = cache_from("api-modules")
  cache-to = cache_to("api-modules")
}

target "postgres-distribution" {
  inherits = ["_cache-only"]
  context = "infrastructure/postgres"
  dockerfile = "Dockerfile"
  target = "cache-ready"
  no-cache-filter = ["cache-ready"]
  cache-from = cache_from("postgres-distribution")
  cache-to = cache_to("postgres-distribution")
}
