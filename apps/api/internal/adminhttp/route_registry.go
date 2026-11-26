package adminhttp

import "net/http"

// RegisterRoutes mounts the paths available in the private OpenAPI document.
// The administration handler remains responsible for methods and authorization.
func RegisterRoutes(mux *http.ServeMux, handler http.Handler) {
	for path := range privateRoutes() {
		mux.Handle(path, handler)
	}
	// Preserve the administration error boundary for unmatched nested paths.
	for _, prefix := range []string{authPrefix, usersPath, rolesPath, accountPath, accountSessionsPath} {
		mux.Handle(prefix+"/", handler)
	}
}

func privateRoutes() map[string][]route {
	all := make(map[string][]route, len(routes)+len(userRoutes))
	for path, rt := range routes {
		all[authPrefix+path] = []route{rt}
	}
	for path, methods := range userRoutes {
		all[path] = methods
	}
	for path, methods := range roleRoutes {
		all[path] = methods
	}
	all[sessionPolicyPath] = sessionPolicyRoutes
	all[ratePolicyPath] = ratePolicyRoutes
	all[roleGrantOptionsPath] = []route{readRoleGrantOptionsRoute}
	all[contentAssigneesPath] = []route{readContentAssigneesRoute, replaceContentAssigneesRoute}
	all[createDraftPath] = collectionDraftRoutes
	all[contentDraftPath] = itemDraftRoutes
	all[contentCollectionsPath] = []route{listCollectionsRoute, registryCreateRoute}
	all[registryCollectionPath] = []route{registryReadRoute, registryReviseRoute}
	all[registryAvailabilityPath] = []route{registryAvailabilityRoute}
	all[registryIconPath] = []route{registryIconRoute}
	all[registryFieldChangesPath] = []route{registryFieldChangesRoute}
	all[registryRenderersPath] = []route{registryRendererRoute}
	all[draftMetadataPath] = []route{readDraftMetadataRoute}
	for path, rt := range draftCandidateRoutes {
		all[path] = []route{rt}
	}
	for path, methods := range settingsRoutes {
		all[path] = methods
	}
	for path, methods := range adminUIRoutes {
		all[path] = methods
	}
	for path, rt := range accountRoutes {
		all[path] = []route{rt}
	}
	return all
}
