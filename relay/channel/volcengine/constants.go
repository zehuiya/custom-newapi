package volcengine

var ModelList = []string{
	"Doubao-pro-128k",
	"Doubao-pro-32k",
	"Doubao-pro-4k",
	"Doubao-lite-128k",
	"Doubao-lite-32k",
	"Doubao-lite-4k",
	"Doubao-embedding",
	"doubao-seedream-4-0-250828",
	"seedream-4-0-250828",
	"doubao-seedance-1-0-pro-250528",
	"seedance-1-0-pro-250528",
	"doubao-seed-1-6-thinking-250715",
	"seed-1-6-thinking-250715",
}

var ChannelName = "volcengine"

// AgentPlanModelList contains the stable aliases exposed by Volcengine Agent Plan.
// Keep this separate from ModelList because Agent Plan uses a dedicated API path
// and subscription key, and the aliases are not regular Ark endpoint IDs.
var AgentPlanModelList = []string{
	// Text generation
	"doubao-seed-2.0-mini",
	"doubao-seed-2.0-lite",
	"deepseek-v4-flash",
	"doubao-seed-evolving",
	"doubao-seed-2.0-code",
	"doubao-seed-2.0-pro",
	"minimax-m2.7",
	"minimax-m3",
	"glm-5.2",
	"glm-latest",
	"kimi-k2.6",
	"kimi-k2.7-code",
	"deepseek-v4-pro",
	"kimi-k3",

	// Embeddings
	"doubao-embedding-vision",

	// Image generation
	"doubao-seedream-5.0-lite",

	// Video generation
	"doubao-seedance-1.5-pro",
	"doubao-seedance-2.0",
	"doubao-seedance-2.0-fast",
	"doubao-seedance-2.0-mini",
}

var AgentPlanVideoModelList = []string{
	"doubao-seedance-1.5-pro",
	"doubao-seedance-2.0",
	"doubao-seedance-2.0-fast",
	"doubao-seedance-2.0-mini",
}

const AgentPlanChannelName = "volcengine-agent-plan"
