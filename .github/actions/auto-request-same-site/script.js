const { Octokit } = require("@octokit/rest");
const { createSlackUtils } = require("../slack-utils");
const fs = require("fs");
const path = require("path");

const token = process.env.APP_TOKEN;
const org = process.env.ORG;
const sf = process.env.SF_TEAM_SLUG;
const prg = process.env.PRG_TEAM_SLUG;
const n = Math.max(1, parseInt(process.env.REVIEWERS_TO_REQUEST || "1", 10));

const gh = new Octokit({ auth: token });
const [owner, repo] = process.env.GITHUB_REPOSITORY.split("/");

// Slack configuration using shared utils
const slackBotToken = process.env.SLACK_BOT_TOKEN?.trim() || null;
const slackUtils = createSlackUtils({
  githubToken: token,
  slackBotToken,
  org,
  emailDomain: "e2b.dev",
});

function getEvent() {
  return JSON.parse(fs.readFileSync(process.env.GITHUB_EVENT_PATH, "utf8"));
}

function prNumber(ev) {
  // Support manual trigger via workflow_dispatch
  if (process.env.PR_NUMBER) {
    return parseInt(process.env.PR_NUMBER, 10);
  }
  return ev.pull_request?.number ?? null;
}

async function getPR(num) {
  const { data } = await gh.pulls.get({ owner, repo, pull_number: num });
  return data;
}

async function getUserTeams(login) {
  const data = await gh.graphql(
    `query($org:String!, $login:String!){
      organization(login:$org){
        teams(first:100, userLogins: [$login]){ nodes { slug } }
      }
    }`,
    { org, login }
  );
  return (data.organization?.teams?.nodes || []).map((t) => t.slug);
}

async function listTeamMembers(teamSlug) {
  const res = await gh.teams.listMembersInOrg({ org, team_slug: teamSlug, per_page: 100 });
  return res.data.map((u) => u.login);
}

async function notifySlackUsers(assignees, prNumber, prUrl, prTitle) {
  if (!slackUtils.isSlackConfigured()) {
    console.log("Slack not configured; skipping notifications.");
    return;
  }

  for (const assignee of assignees) {
    const text = `You've been assigned to review PR #${prNumber}: ${prTitle}\n${prUrl}`;
    const blocks = [
      {
        type: "section",
        text: {
          type: "mrkdwn",
          text: `You've been assigned to review *<${prUrl}|PR #${prNumber}>*`,
        },
      },
      {
        type: "section",
        text: {
          type: "mrkdwn",
          text: `*${prTitle}*`,
        },
      },
    ];

    const sent = await slackUtils.sendDirectMessageToGitHubUser(assignee, text, blocks);
    if (sent) {
      console.log(`Sent Slack DM to ${assignee}`);
    } else {
      console.log(`Skipping Slack notification for ${assignee} (no Slack user found)`);
    }
  }
}

function pickRandom(arr, k) {
  const list = [...arr];
  for (let i = list.length - 1; i > 0; i--) {
    const j = Math.floor(Math.random() * (i + 1));
    [list[i], list[j]] = [list[j], list[i]];
  }
  return list.slice(0, Math.max(0, Math.min(k, list.length)));
}

function parseCodeowners(content) {
  const lines = content.split("\n");
  const owners = new Set();

  for (const line of lines) {
    const trimmed = line.trim();
    if (!trimmed || trimmed.startsWith("#")) continue;

    // Match @username patterns, skip team patterns like @org/team
    const matches = trimmed.matchAll(/@(\S+)/g);
    for (const match of matches) {
      const owner = match[1];
      if (!owner.includes("/")) {
        owners.add(owner);
      }
    }
  }

  return Array.from(owners);
}

(async () => {
  const ev = getEvent();
  const num = prNumber(ev);
  if (!num) {
    console.log("No PR number in event; exiting.");
    return;
  }

  const pr = await getPR(num);
  const author = pr.user.login;
  const alreadyAssigned = new Set((pr.assignees || []).map((a) => a.login));

  const teams = await getUserTeams(author);
  const site = teams.includes(sf) ? sf : teams.includes(prg) ? prg : null;

  let assignees;

  if (!site) {
    console.log("Author not in configured teams; assigning from CODEOWNERS.");

    // Read and parse CODEOWNERS file
    let codeownersContent;
    const repoRoot = process.env.GITHUB_WORKSPACE || process.cwd();
    const codeownersPath = path.join(repoRoot, "CODEOWNERS");
    try {
      codeownersContent = fs.readFileSync(codeownersPath, "utf8");
    } catch (error) {
      console.log(`Failed to read CODEOWNERS file at ${codeownersPath}: ${error.message}`);
      return;
    }

    const codeowners = parseCodeowners(codeownersContent);

    if (!codeowners.length) {
      console.log("No CODEOWNERS found; skipping assignee update.");
      return;
    }

    const codeownerSet = new Set(codeowners);
    const assignedFromCodeowners = [...alreadyAssigned].filter((login) => codeownerSet.has(login));

    if (assignedFromCodeowners.length >= n) {
      console.log(`PR #${num} already has ${assignedFromCodeowners.length} CODEOWNER assignee(s); nothing to do.`);
      return;
    }

    const needed = n - assignedFromCodeowners.length;
    const candidates = codeowners.filter((owner) => owner !== author && !alreadyAssigned.has(owner));

    if (!candidates.length) {
      console.log("All CODEOWNERS are either the author or already assigned; nothing to add.");
      return;
    }

    assignees = pickRandom(candidates, needed);
    if (!assignees.length) {
      console.log("Unable to select assignees from CODEOWNERS; skipping.");
      return;
    }
  } else {
    const siteMembers = (await listTeamMembers(site)).filter((user) => user !== author);
    if (!siteMembers.length) {
      console.log(`No teammates found in ${site}; skipping assignee update.`);
      return;
    }

    const siteMemberSet = new Set(siteMembers);
    const assignedFromSite = [...alreadyAssigned].filter((login) => siteMemberSet.has(login));

    if (assignedFromSite.length >= n) {
      console.log(`PR #${num} already has ${assignedFromSite.length} teammate assignee(s); nothing to do.`);
      return;
    }

    const needed = n - assignedFromSite.length;

    const candidates = siteMembers.filter((member) => !alreadyAssigned.has(member));
    if (!candidates.length) {
      console.log(`All teammates from ${site} are already assigned; nothing to add.`);
      return;
    }

    assignees = pickRandom(candidates, needed);
    if (!assignees.length) {
      console.log(`Unable to select additional assignees from ${site}; skipping.`);
      return;
    }
  }

  await gh.issues.addAssignees({
    owner,
    repo,
    issue_number: num,
    assignees,
  });

  console.log(`Assigned ${assignees.join(", ")} to PR #${num}.`);

  // Send Slack notifications
  await notifySlackUsers(assignees, num, pr.html_url, pr.title);
})().catch((error) => {
  console.error(error);
  process.exit(1);
});
