package jumpcloud

import "strings"

// deriveCategory assigns a SaaS application to a coarse purpose bucket from its
// name and domains.
//
// This is a DERIVED heuristic: the JumpCloud public SaaS API does not expose its
// own category taxonomy (the catalog returns only name/description/domains/logo),
// so we approximate "group by purpose" with a keyword match. It is intentionally
// simple and best-effort — unmatched apps fall through to "Other".
//
// Buckets are checked in order; the first matching keyword wins, so more
// specific buckets should precede broad ones.
func deriveCategory(name string, domains []string) string {
	hay := strings.ToLower(name)
	for _, d := range domains {
		hay += " " + strings.ToLower(d)
	}

	for _, b := range _categoryBuckets {
		for _, kw := range b.keywords {
			if strings.Contains(hay, kw) {
				return b.name
			}
		}
	}
	return "Other"
}

type categoryBucket struct {
	name     string
	keywords []string
}

// _categoryBuckets is the ordered keyword map. Order matters: AI and Security
// are checked early so e.g. "github copilot" lands in AI and "okta" in Security
// before broader buckets could claim them.
var _categoryBuckets = []categoryBucket{
	{"AI", []string{
		"openai", "chatgpt", "anthropic", "claude", "copilot", "midjourney",
		"perplexity", "huggingface", "hugging face", "gemini", "mistral",
		"stability.ai", "stability ai", "runway", "elevenlabs", "replicate",
	}},
	{"Security/IAM", []string{
		"okta", "onelogin", "1password", "lastpass", "bitwarden", "dashlane",
		"duo", "crowdstrike", "sentinelone", "snyk", "tenable", "qualys",
		"cloudflare", "auth0", "yubico", "knowbe4",
	}},
	{"Dev Tools", []string{
		"github", "gitlab", "bitbucket", "jira", "jenkins", "circleci",
		"travis", "npm", "docker", "vercel", "netlify", "sentry", "datadog",
		"pagerduty", "postman", "sonarqube", "jetbrains", "linear", "raygun",
		"new relic", "newrelic", "launchdarkly",
	}},
	{"Cloud/Infra", []string{
		"aws", "amazon web", "azure", "gcp", "google cloud", "digitalocean",
		"heroku", "linode", "vultr", "terraform", "hashicorp", "fastly",
		"mongodb", "snowflake", "databricks",
	}},
	{"Communication", []string{
		"slack", "zoom", "microsoft teams", "msteams", "google meet", "webex",
		"discord", "mattermost", "ringcentral", "twilio", "dialpad", "vonage",
	}},
	{"Email", []string{
		"gmail", "outlook", "proton", "fastmail", "sendgrid", "mailgun",
		"postmark",
	}},
	{"Design", []string{
		"figma", "sketch", "canva", "adobe", "invision", "miro", "framer",
		"lucidchart", "lucid", "zeplin",
	}},
	{"Productivity", []string{
		"notion", "confluence", "asana", "trello", "monday.com", "monday ",
		"clickup", "airtable", "evernote", "todoist", "basecamp", "smartsheet",
		"coda", "wrike",
	}},
	{"Storage/Files", []string{
		"dropbox", "box.com", "onedrive", "sharepoint", "google drive",
		"egnyte", "wetransfer",
	}},
	{"CRM/Sales", []string{
		"salesforce", "hubspot", "pipedrive", "zoho", "intercom", "outreach",
		"gong", "salesloft", "close.com", "copper",
	}},
	{"Finance/HR", []string{
		"quickbooks", "xero", "gusto", "workday", "bamboohr", "expensify",
		"brex", "ramp", "stripe", "bill.com", "netsuite", "rippling", "deel",
		"paychex", "adp",
	}},
	{"Marketing", []string{
		"mailchimp", "hootsuite", "semrush", "ahrefs", "marketo", "klaviyo",
		"buffer", "sprout", "hubspot marketing", "constant contact",
	}},
	{"Analytics", []string{
		"tableau", "looker", "mixpanel", "amplitude", "segment",
		"google analytics", "hotjar", "heap", "power bi", "powerbi", "metabase",
	}},
	{"Support", []string{
		"zendesk", "freshdesk", "freshservice", "helpscout", "help scout",
		"servicenow",
	}},
}
