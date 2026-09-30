package api

// chatgptPluginLogoPNG is a 1x1 transparent PNG served for the manifest logo
// field. Embedding the bytes keeps the plugin self-contained: no binary asset
// to ship and the manifest always resolves to a valid image.
var chatgptPluginLogoPNG = []byte("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6" +
	"kgAAAABJRU5ErkJggg==")
