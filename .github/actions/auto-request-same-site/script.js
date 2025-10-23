const { Octokit } = require("@octokit/rest");
const { WebClient } = require("@slack/web-api");
const fs = require("fs");

const token = process.env.APP_TOKEN;
const org = process.env.ORG;
const sf = process.env.SF_TEAM_SLUG;
const prg = process.env.PRG_TEAM_SLUG;
const n = Math.max(1, parseInt(process.env.REVIEWERS_TO_REQUEST || "1", 10));

const gh = new Octokit({ auth: token });
const [owner, repo] = process.env.GITHUB_REPOSITORY.split("/");

// Slack configuration
const slackToken = process.env.SLACK_BOT_TOKEN;
const slack = slackToken ? new WebClient(slackToken) : null;

function getEvent() {
  return JSON.parse(fs.readFileSync(process.env.GITHUB_EVENT_PATH, "utf8"));
}

function prNumber(ev) {
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
    { org, login },
  );
  return (data.organization?.teams?.nodes || []).map((t) => t.slug);
}

async function listTeamMembers(teamSlug) {
  const res = await gh.teams.listMembersInOrg({ org, team_slug: teamSlug, per_page: 100 });
  return res.data.map((u) => u.login);
}

async function getGitHubUserEmail(username) {
  try {
    const { data } = await gh.users.getByUsername({ username });
    return data.email || null;
  } catch (error) {
    console.log(`Failed to fetch email for ${username}: ${error.message}`);
    return null;
  }
}

async function getSlackUserId(githubUsername) {
  if (!slack) {
    return null;
  }

  const email = await getGitHubUserEmail(githubUsername);
  if (!email || !email.endsWith("@e2b.dev")) {
    console.log(`No @e2b.dev email found for ${githubUsername}`);
    return null;
  }

  try {
    const result = await slack.users.lookupByEmail({ email });
    if (result.ok && result.user?.id) {
      console.log(`Found Slack user ${result.user.id} for ${githubUsername} via ${email}`);
      return result.user.id;
    }
  } catch (error) {
    console.log(`Slack lookup failed for ${githubUsername} (${email}): ${error.message}`);
  }

  return null;
}

async function notifySlackUsers(assignees, prNumber, prUrl, prTitle) {
  if (!slack) {
    console.log("Slack not configured; skipping notifications.");
    return;
  }

  for (const assignee of assignees) {
    const slackUserId = await getSlackUserId(assignee);
    if (!slackUserId) {
      console.log(`Skipping Slack notification for ${assignee} (no Slack user found)`);
      continue;
    }

    try {
      await slack.chat.postMessage({
        channel: slackUserId,
        text: `You've been assigned to review PR #${prNumber}: ${prTitle}\n${prUrl}`,
        blocks: [
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
        ],
      });
      console.log(`Sent Slack DM to ${assignee} (${slackUserId})`);
    } catch (error) {
      console.log(`Failed to send Slack DM to ${assignee}: ${error.message}`);
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

    // Match @username patterns
    const matches = trimmed.matchAll(/@(\S+)/g);
    for (const match of matches) {
      owners.add(match[1]);
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
    try {
      codeownersContent = fs.readFileSync("CODEOWNERS", "utf8");
    } catch (error) {
      console.log(`Failed to read CODEOWNERS file: ${error.message}`);
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
