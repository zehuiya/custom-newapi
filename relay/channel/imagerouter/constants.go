package imagerouter

const (
	ChannelName    = "ImageRouter"
	DefaultBaseURL = "https://api.imagerouter.io"

	ModelQwenImage2512         = "qwen-image-2512"
	UpstreamModelQwenImage2512 = "qwen/qwen-image-2512"
)

var ModelList = []string{
	ModelQwenImage2512,
}
