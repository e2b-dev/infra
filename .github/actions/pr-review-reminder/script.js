const { Octokit } = require("@octokit/rest");
const { createSlackUtils } = require("../slack-utils");

const githubToken = process.env.GITHUB_TOKEN;
const slackBotToken = process.env.SLACK_BOT_TOKEN;
const org = process.env.ORG || "e2b-dev";
const staleDays = parseInt(process.env.STALE_DAYS || "2", 10);
const dryRun = process.env.DRY_RUN === "true";

const [owner, repo] = process.env.GITHUB_REPOSITORY.split("/");
const gh = new Octokit({ auth: githubToken });

const slackUtils = createSlackUtils({
  githubToken,
  slackBotToken,
  org,
  emailDomain: "e2b.dev",
});

async function main() {
  console.log(`Configuration: staleDays=${staleDays}, dryRun=${dryRun}, org=${org}`);

  if (!slackUtils.isSlackConfigured()) {
    console.log("Slack not configured (SLACK_BOT_TOKEN missing). Exiting.");
    return;
  }

  // Get all open PRs
  const { data: prs } = await gh.pulls.list({
    owner,
    repo,
    state: "open",
    per_page: 100,
  });

  console.log(`Found ${prs.length} open PRs`);

  const now = new Date();
  const reviewerPRs = new Map(); // Map<reviewer, Array<PR>>

  for (const pr of prs) {
    // Skip draft PRs
    if (pr.draft) {
      console.log(`PR #${pr.number}: Skipping (draft)`);
      continue;
    }

    // Check if PR has requested reviewers
    const requestedReviewers = pr.requested_reviewers.map((r) => r.login);
    const requestedTeams = pr.requested_teams.map((t) => t.name);

    if (requestedReviewers.length === 0 && requestedTeams.length === 0) {
      console.log(`PR #${pr.number}: Skipping (no reviewers requested)`);
      continue;
    }

    // Calculate days since last update
    const updatedAt = new Date(pr.updated_at);
    const daysSinceUpdate = Math.floor((now - updatedAt) / (1000 * 60 * 60 * 24));

    if (daysSinceUpdate < staleDays) {
      console.log(`PR #${pr.number}: Not stale (${daysSinceUpdate} days)`);
      continue;
    }

    console.log(`PR #${pr.number}: Stale (${daysSinceUpdate} days), reviewers: ${requestedReviewers.join(", ")}`);

    // Group PRs by reviewer
    for (const reviewer of requestedReviewers) {
      if (!reviewerPRs.has(reviewer)) {
        reviewerPRs.set(reviewer, []);
      }
      reviewerPRs.get(reviewer).push({
        number: pr.number,
        title: pr.title,
        author: pr.user.login,
        url: pr.html_url,
        daysSinceUpdate,
      });
    }

    // Note: We don't DM teams, only individual reviewers
    if (requestedTeams.length > 0) {
      console.log(`PR #${pr.number}: Has team reviewers (${requestedTeams.join(", ")}), but only DMing individuals`);
    }
  }

  if (reviewerPRs.size === 0) {
    console.log("No stale PRs with pending reviewers. Nothing to notify.");
    return;
  }

  // Send DMs to each reviewer
  for (const [reviewer, prList] of reviewerPRs) {
    const prSummary = prList
      .map((pr) => `  *<${pr.url}|#${pr.number}>*: ${pr.title} (by ${pr.author}, ${pr.daysSinceUpdate}d waiting)`)
      .join("\n");

    const text = `You have ${prList.length} PR${prList.length > 1 ? "s" : ""} waiting for your review:\n\n${prSummary}`;

    const blocks = [
      {
        type: "section",
        text: {
          type: "mrkdwn",
          text: `:eyes: *PR Review Reminder*\n\nYou have ${prList.length} PR${prList.length > 1 ? "s" : ""} waiting for your review:`,
        },
      },
      {
        type: "divider",
      },
      ...prList.map((pr) => ({
        type: "section",
        text: {
          type: "mrkdwn",
          text: `*<${pr.url}|#${pr.number}: ${pr.title}>*\nby ${pr.author} | waiting ${pr.daysSinceUpdate} days`,
        },
      })),
    ];

    if (dryRun) {
      console.log(`[DRY RUN] Would send to ${reviewer}:\n${text}\n`);
      continue;
    }

    const sent = await slackUtils.sendDirectMessageToGitHubUser(reviewer, text, blocks);
    if (sent) {
      console.log(`Sent reminder to ${reviewer} for ${prList.length} PR(s)`);
    } else {
      console.log(`Failed to send reminder to ${reviewer}`);
    }
  }

  console.log("Done sending reminders.");
}

main().catch((error) => {
  console.error(error);
  process.exit(1);
});
