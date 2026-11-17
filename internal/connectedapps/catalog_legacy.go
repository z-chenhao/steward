package connectedapps

// Legacy editorial interpretation for pre-service compositions, explicit
// direct/unbundled Service compositions and installations predating this service.
// Remove after managed routing and retained-consumer inventory prove these
// exact data/lookup semantics are no longer required; never a managed fallback.

// These are discovery suggestions, not a promise that a particular project's
// managed OAuth configuration is available. The live catalogue is authoritative
// and remains paginated/searchable beyond this convenience list.
type suggestion struct{ slug, name, category string }

var suggestions = []suggestion{
	{"gmail", "Gmail", "communication"}, {"outlook", "Outlook", "communication"}, {"slack", "Slack", "communication"}, {"microsoft_teams", "Microsoft Teams", "communication"}, {"discord", "Discord", "communication"}, {"telegram", "Telegram", "communication"}, {"whatsapp", "WhatsApp", "communication"}, {"zoom", "Zoom", "communication"},
	{"googlecalendar", "Google Calendar", "productivity"}, {"googletasks", "Google Tasks", "productivity"}, {"notion", "Notion", "productivity"}, {"todoist", "Todoist", "productivity"}, {"ticktick", "TickTick", "productivity"}, {"asana", "Asana", "productivity"}, {"trello", "Trello", "productivity"}, {"clickup", "ClickUp", "productivity"}, {"monday", "Monday.com", "productivity"}, {"airtable", "Airtable", "productivity"}, {"calendly", "Calendly", "productivity"},
	{"googledrive", "Google Drive", "storage"}, {"googledocs", "Google Docs", "storage"}, {"googlesheets", "Google Sheets", "storage"}, {"googleslides", "Google Slides", "storage"}, {"one_drive", "OneDrive", "storage"}, {"share_point", "SharePoint", "storage"}, {"dropbox", "Dropbox", "storage"}, {"box", "Box", "storage"}, {"confluence", "Confluence", "storage"},
	{"github", "GitHub", "development"}, {"gitlab", "GitLab", "development"}, {"bitbucket", "Bitbucket", "development"}, {"linear", "Linear", "development"}, {"jira", "Jira", "development"}, {"sentry", "Sentry", "development"}, {"vercel", "Vercel", "development"}, {"netlify", "Netlify", "development"}, {"supabase", "Supabase", "development"},
	{"figma", "Figma", "design"}, {"canva", "Canva", "design"}, {"miro", "Miro", "design"}, {"webflow", "Webflow", "design"}, {"wordpress", "WordPress", "design"},
	{"hubspot", "HubSpot", "business"}, {"salesforce", "Salesforce", "business"}, {"pipedrive", "Pipedrive", "business"}, {"zendesk", "Zendesk", "business"}, {"intercom", "Intercom", "business"}, {"mailchimp", "Mailchimp", "business"}, {"typeform", "Typeform", "business"}, {"shopify", "Shopify", "business"}, {"stripe", "Stripe", "business"}, {"quickbooks", "QuickBooks", "business"}, {"xero", "Xero", "business"},
	{"youtube", "YouTube", "personal"}, {"spotify", "Spotify", "personal"}, {"reddit", "Reddit", "personal"}, {"twitter", "X (Twitter)", "personal"}, {"linkedin", "LinkedIn", "personal"}, {"instagram", "Instagram", "personal"}, {"pinterest", "Pinterest", "personal"}, {"raindrop", "Raindrop.io", "personal"}, {"strava", "Strava", "personal"}, {"fitbit", "Fitbit", "personal"}, {"google_maps", "Google Maps", "personal"}, {"weathermap", "OpenWeatherMap", "personal"},
}
