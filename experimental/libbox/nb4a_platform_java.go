package libbox

var intfBox PlatformInterface
var intfNB4A NB4AInterface

type NB4AInterface interface {
	UseOfficialAssets() bool
	Selector_OnProxySelected(selectorTag string, tag string)
}
