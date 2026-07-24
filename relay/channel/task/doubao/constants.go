package doubao

var ModelList = []string{
	"doubao-seedance-1-0-pro-250528",
	"doubao-seedance-1-0-lite-t2v",
	"doubao-seedance-1-0-lite-i2v",
	"doubao-seedance-1-5-pro-251215",
	"doubao-seedance-1-0-pro-fast-251015",
	"doubao-seedance-2-0-260128",
	"doubao-seedance-2-0-fast-260128",
	"doubao-seedance-2-0-mini-260615",
}

var ChannelName = "doubao-video"

// SeedanceBillingConfig 定义单个 Seedance 模型的计费配置
type SeedanceBillingConfig struct {
	// BaseScenario 基准场景描述（用于管理员理解应该配置什么作为基准倍率）
	BaseScenario string
	// SupportResolution 是否支持分辨率差异化计费
	SupportResolution bool
	// SupportVideoInput 是否支持视频输入差异化计费
	SupportVideoInput bool
	// SupportAudio 是否支持音频差异化计费
	SupportAudio bool
	// ResolutionRatios 分辨率倍率映射（相对于基准）
	ResolutionRatios map[string]float64
	// VideoInputRatio 有视频输入时的折扣（相对于无视频）
	VideoInputRatio float64
	// VideoInputRatios 按分辨率配置有视频输入时的折扣（相对于同分辨率无视频）
	VideoInputRatios map[string]float64
	// NoAudioRatio 无声视频的折扣（相对于有声）
	NoAudioRatio float64
}

// seedanceBillingConfigs 各模型的计费配置
// 管理员应为每个模型配置"基准场景"的倍率，系统会根据实际请求参数自动调整
var seedanceBillingConfigs = map[string]SeedanceBillingConfig{
	"doubao-seedance-2-0-260128": {
		BaseScenario:      "480p/720p 不含视频输入",
		SupportResolution: true,
		SupportVideoInput: true,
		SupportAudio:      false,
		ResolutionRatios: map[string]float64{
			"480p":  1.0,         // 基准
			"720p":  1.0,         // 与 480p 同价
			"1080p": 51.0 / 46.0, // 51元 / 46元 ≈ 1.109
		},
		VideoInputRatio: 28.0 / 46.0, // 未知分辨率沿用 480p/720p 折扣
		VideoInputRatios: map[string]float64{
			"480p":  28.0 / 46.0, // 含视频 28元 / 不含视频 46元
			"720p":  28.0 / 46.0,
			"1080p": 31.0 / 51.0, // 含视频 31元 / 不含视频 51元
		},
	},
	"doubao-seedance-2-0-fast-260128": {
		BaseScenario:      "480p/720p 不含视频输入",
		SupportResolution: true,
		SupportVideoInput: true,
		SupportAudio:      false,
		ResolutionRatios: map[string]float64{
			"480p": 1.0, // 基准
			"720p": 1.0, // 与 480p 同价
			// 2.0-fast 不支持 1080p
		},
		VideoInputRatio: 22.0 / 37.0, // 含视频：22元，不含：37元 ≈ 0.595
	},
	"doubao-seedance-2-0-mini-260615": {
		BaseScenario:      "不含视频输入",
		SupportVideoInput: true,
		VideoInputRatio:   14.0 / 23.0, // 含视频：14元，不含：23元 ≈ 0.609
	},
	"doubao-seedance-1-5-pro-251215": {
		BaseScenario:      "有声视频",
		SupportResolution: false,
		SupportVideoInput: false,
		SupportAudio:      true,
		NoAudioRatio:      8.0 / 16.0, // 无声：8元，有声：16元 = 0.5
	},
	"doubao-seedance-1-0-pro-250528": {
		BaseScenario: "统一价格 15元/百万token",
		// 1.0 pro 无差异化计费
	},
	"doubao-seedance-1-0-pro-fast-251015": {
		BaseScenario: "统一价格 4.2元/百万token",
		// 1.0 pro fast 无差异化计费
	},
	"doubao-seedance-1-0-lite-i2v": {
		BaseScenario: "Lite 版本统一价格",
		// Lite 版本无差异化计费
	},
	"doubao-seedance-1-0-lite-t2v": {
		BaseScenario: "Lite 版本统一价格",
		// Lite 版本无差异化计费
	},
}

// Agent Plan exposes stable model aliases while the regular Ark API uses
// versioned model IDs. Map aliases only when both products have a confirmed
// shared billing baseline; otherwise keep their pricing independent.
var seedanceBillingAliases = map[string]string{
	"doubao-seedance-1.5-pro":  "doubao-seedance-1-5-pro-251215",
	"doubao-seedance-2.0":      "doubao-seedance-2-0-260128",
	"doubao-seedance-2.0-fast": "doubao-seedance-2-0-fast-260128",
}

// GetSeedanceBillingConfig 获取指定模型的计费配置
func GetSeedanceBillingConfig(modelName string) (SeedanceBillingConfig, bool) {
	if canonicalModel, ok := seedanceBillingAliases[modelName]; ok {
		modelName = canonicalModel
	}
	config, ok := seedanceBillingConfigs[modelName]
	return config, ok
}
