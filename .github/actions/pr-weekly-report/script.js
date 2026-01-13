const { Octokit } = require("@octokit/rest");
const { createSlackUtils } = require("../slack-utils");

const githubToken = process.env.GITHUB_TOKEN;
const slackBotToken = process.env.SLACK_BOT_TOKEN;
const slackChannel = process.env.SLACK_CHANNEL;
const org = process.env.ORG || "e2b-dev";
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
  console.log(`Configuration: dryRun=${dryRun}, org=${org}, channel=${slackChannel || "(not set)"}`);

  if (!slackUtils.isSlackConfigured()) {
    console.log("Slack not configured (SLACK_BOT_TOKEN missing). Exiting.");
    return;
  }

  if (!slackChannel) {
    console.log("SLACK_CHANNEL not set. Exiting.");
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

  if (prs.length === 0) {
    const text = ":white_check_mark: *Weekly PR Report*\n\nNo open PRs this week. Great job keeping things clean!";

    if (dryRun) {
      console.log(`[DRY RUN] Would send to channel:\n${text}`);
      return;
    }

    await slackUtils.sendChannelMessage(slackChannel, text);
    return;
  }

  // Categorize PRs
  const drafts = [];
  const needsReview = [];
  const inReview = [];
  const approved = [];

  for (const pr of prs) {
    const createdAt = new Date(pr.created_at);
    const daysSinceCreation = Math.floor((now - createdAt) / (1000 * 60 * 60 * 24));

    const requestedReviewers = pr.requested_reviewers.map((r) => r.login);
    const requestedTeams = pr.requested_teams.map((t) => t.name);

    const prInfo = {
      number: pr.number,
      title: pr.title.length > 60 ? pr.title.substring(0, 57) + "..." : pr.title,
      author: pr.user.login,
      url: pr.html_url,
      daysSinceCreation,
      reviewers: [...requestedReviewers, ...requestedTeams],
    };

    if (pr.draft) {
      drafts.push(prInfo);
    } else if (requestedReviewers.length === 0 && requestedTeams.length === 0) {
      needsReview.push(prInfo);
    } else {
      // Check if PR has any reviews
      const { data: reviews } = await gh.pulls.listReviews({
        owner,
        repo,
        pull_number: pr.number,
      });

      const hasApproval = reviews.some((r) => r.state === "APPROVED");
      if (hasApproval) {
        approved.push(prInfo);
      } else {
        inReview.push(prInfo);
      }
    }
  }

  // Build report with Slack user mentions
  async function formatPR(pr) {
    const authorMention = await slackUtils.formatUserMention(pr.author);
    const reviewerMentions = await Promise.all(pr.reviewers.map((r) => slackUtils.formatUserMention(r)));
    const reviewersStr = reviewerMentions.length > 0 ? `, reviewers: ${reviewerMentions.join(", ")}` : "";

    return `  *<${pr.url}|#${pr.number}>* - ${pr.title}\n    by ${authorMention} (${pr.daysSinceCreation}d old${reviewersStr})`;
  }

  let report = `:clipboard: *Weekly PR Report*\n`;
  report += `_${now.toDateString()}_\n\n`;
  report += `*Summary:* ${prs.length} open PRs\n`;
  report += `  :white_check_mark: Approved: ${approved.length} | :eyes: In Review: ${inReview.length} | :warning: Needs Reviewer: ${needsReview.length} | :construction: Drafts: ${drafts.length}\n\n`;

  if (needsReview.length > 0) {
    report += `:warning: *Needs Reviewers Assigned (${needsReview.length})*\n`;
    const formattedPRs = await Promise.all(needsReview.map(formatPR));
    report += formattedPRs.join("\n\n");
    report += "\n\n";
  }

  if (inReview.length > 0) {
    report += `:eyes: *Awaiting Review (${inReview.length})*\n`;
    const formattedPRs = await Promise.all(inReview.map(formatPR));
    report += formattedPRs.join("\n\n");
    report += "\n\n";
  }

  if (approved.length > 0) {
    report += `:white_check_mark: *Approved - Ready to Merge (${approved.length})*\n`;
    const formattedPRs = await Promise.all(approved.map(formatPR));
    report += formattedPRs.join("\n\n");
    report += "\n\n";
  }

  if (drafts.length > 0) {
    report += `:construction: *Drafts (${drafts.length})*\n`;
    const formattedPRs = await Promise.all(drafts.map(formatPR));
    report += formattedPRs.join("\n\n");
    report += "\n";
  }

  console.log("Report:\n", report);

  if (dryRun) {
    console.log("[DRY RUN] Would send report to channel.");
    return;
  }

  const sent = await slackUtils.sendChannelMessage(slackChannel, report);
  if (sent) {
    console.log("Weekly report sent successfully.");
  } else {
    console.log("Failed to send weekly report.");
    process.exit(1);
  }
}

main().catch((error) => {
  console.error(error);
  process.exit(1);
});
