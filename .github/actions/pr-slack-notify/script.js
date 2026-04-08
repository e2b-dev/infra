const { WebClient } = require("@slack/web-api");
const fs = require("fs");

const slackToken = process.env.SLACK_BOT_TOKEN;
const channelId = process.env.SLACK_CHANNEL_ID;
const reviewGroup = process.env.SLACK_REVIEW_GROUP;

if (!slackToken || !channelId || !reviewGroup) {
  console.error("Missing required env vars: SLACK_BOT_TOKEN, SLACK_CHANNEL_ID, SLACK_REVIEW_GROUP");
  process.exit(1);
}

const slack = new WebClient(slackToken);

function getEvent() {
  return JSON.parse(fs.readFileSync(process.env.GITHUB_EVENT_PATH, "utf8"));
}

function getPR(ev) {
  // Support manual trigger via workflow_dispatch
  if (process.env.PR_URL) {
    return {
      number: parseInt(process.env.PR_NUMBER, 10),
      title: process.env.PR_TITLE || "PR",
      html_url: process.env.PR_URL,
      user: { login: process.env.PR_AUTHOR || "unknown" },
    };
  }

  const pr = ev.pull_request;
  if (!pr) return null;

  return {
    number: pr.number,
    title: pr.title,
    html_url: pr.html_url,
    user: { login: pr.user.login },
  };
}

(async () => {
  const ev = getEvent();
  const pr = getPR(ev);

  if (!pr) {
    console.log("No PR in event; exiting.");
    return;
  }

  // Skip draft PRs
  if (ev.pull_request?.draft) {
    console.log(`PR #${pr.number} is a draft; skipping.`);
    return;
  }

  // Escape Slack mrkdwn control characters to prevent injection via PR title.
  // A fork contributor could craft a title with > or <!everyone> to break
  // formatting or trigger channel-wide pings.
  const safeTitle = pr.title
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;");

  const message = `<!subteam^${reviewGroup}> — new PR needs review\n*<${pr.html_url}|#${pr.number} — ${safeTitle}>* by ${pr.user.login}`;

  await slack.chat.postMessage({
    channel: channelId,
    text: message,
    unfurl_links: false,
    unfurl_media: false,
  });

  console.log(`Posted PR #${pr.number} to channel ${channelId}.`);
})().catch((error) => {
  console.error(error);
  process.exit(1);
});
