package naming

// hstsTLDs are the HSTS-preloaded top-level domains. A service id is also a
// hostname on the dev network ("http://rails:3000"); a browser treats a bare
// "app" or "dev" as that TLD and forces HTTPS on it, so such ids are refused.
// Taken from the entries without a dot in Chromium's
// net/http/transport_security_state_static.json at commit
// 1f1b0c3d214f65331353fd0f1943f8dec1b11dc7 (all "force-https",
// include_subdomains).
var hstsTLDs = map[string]bool{
	"amazon": true, "android": true, "app": true, "audible": true, "azure": true, "bank": true,
	"bing": true, "boo": true, "channel": true, "chrome": true, "dad": true, "day": true,
	"dev": true, "eat": true, "esq": true, "fire": true, "fly": true, "foo": true,
	"fujitsu": true, "gle": true, "gmail": true, "google": true, "hangout": true, "here": true,
	"hotmail": true, "imdb": true, "ing": true, "insurance": true, "kindle": true, "meet": true,
	"meme": true, "microsoft": true, "mov": true, "new": true, "nexus": true, "office": true,
	"page": true, "phd": true, "play": true, "prime": true, "prof": true, "rsvp": true,
	"search": true, "silk": true, "skype": true, "windows": true, "xbox": true, "xn--cckwcxetd": true,
	"xn--jlq480n2rg": true, "youtube": true, "zappos": true, "zip": true,
}
