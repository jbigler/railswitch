package dashconf

// presets holds every preset's light and dark colours, from the spec's
// "Colours" table.
var presets = map[string][2]map[string]string{
	"flexoki": {
		{"bg": "#FFFCF0", "bg2": "#F2F0E5", "sel": "#E6E4D9", "border": "#CECDC3", "text": "#100F0F", "muted": "#6F6E69",
			"accent": "#BC5215", "blue": "#205EA6", "green": "#536907", "yellow": "#8E6B01", "purple": "#5E409D", "red": "#AF3029"},
		{"bg": "#100F0F", "bg2": "#1C1B1A", "sel": "#282726", "border": "#403E3C", "text": "#CECDC3", "muted": "#878580",
			"accent": "#DA702C", "blue": "#4385BE", "green": "#879A39", "yellow": "#D0A215", "purple": "#8B7EC8", "red": "#D14D41"},
	},
	"paper": {
		{"bg": "#FFFFFF", "bg2": "#F4F4F5", "sel": "#E4E4E7", "border": "#D4D4D8", "text": "#18181B", "muted": "#62626B",
			"accent": "#7C3AED", "blue": "#1D4ED8", "green": "#15803D", "yellow": "#A16207", "purple": "#7E22CE", "red": "#B91C1C"},
		{"bg": "#18181B", "bg2": "#27272A", "sel": "#3F3F46", "border": "#52525B", "text": "#F4F4F5", "muted": "#A1A1AA",
			"accent": "#A78BFA", "blue": "#60A5FA", "green": "#4ADE80", "yellow": "#FACC15", "purple": "#C084FC", "red": "#F87171"},
	},
	"solar": {
		{"bg": "#FDF6E3", "bg2": "#EEE8D5", "sel": "#E4DCC4", "border": "#D3CBB2", "text": "#073642", "muted": "#586E75",
			"accent": "#B03F0F", "blue": "#1B6699", "green": "#5C6B00", "yellow": "#7A5C00", "purple": "#6C71C4", "red": "#DC322F"},
		{"bg": "#002B36", "bg2": "#073642", "sel": "#0B4A5A", "border": "#2E5A66", "text": "#EEE8D5", "muted": "#93A1A1",
			"accent": "#CB4B16", "blue": "#268BD2", "green": "#859900", "yellow": "#B58900", "purple": "#6C71C4", "red": "#DC322F"},
	},
	"contrast": {
		{"bg": "#FFFFFF", "bg2": "#F0F0F0", "sel": "#DADADA", "border": "#7A7A7A", "text": "#000000", "muted": "#3D3D3D",
			"accent": "#B3261E", "blue": "#0040A8", "green": "#1E5E00", "yellow": "#6B4E00", "purple": "#6A1B9A", "red": "#C62828"},
		{"bg": "#000000", "bg2": "#121212", "sel": "#2A2A2A", "border": "#8A8A8A", "text": "#FFFFFF", "muted": "#D0D0D0",
			"accent": "#FF8A65", "blue": "#8AB4FF", "green": "#7EE787", "yellow": "#FFD54F", "purple": "#D2A8FF", "red": "#FF7B72"},
	},
}
