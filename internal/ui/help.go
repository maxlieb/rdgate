package ui

import "errors"

// helpLinks are the pages the wizard may open. The wizard names a page by key,
// so the frontend cannot ask the PC to open an arbitrary address.
var helpLinks = map[string]string{
	"oracle-free":  "https://docs.oracle.com/en-us/iaas/Content/FreeTier/resourceref.htm",
	"oracle-learn": "https://docs.oracle.com/en/learn/cloud_free_tier/index.html",
}

// OpenHelp opens one of the known help pages in the browser.
func (a *App) OpenHelp(key string) error {
	u, ok := helpLinks[key]
	if !ok {
		return errors.New("unknown help page")
	}
	return openURL(u)
}
