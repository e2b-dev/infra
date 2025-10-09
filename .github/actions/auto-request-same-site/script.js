const { Octokit } = require("@octokit/rest");
const fs = require("fs");

const token = process.env.APP_TOKEN;
const org = process.env.ORG;
const sf = process.env.SF_TEAM_SLUG;
const prg = process.env.PRG_TEAM_SLUG;
const n = Math.max(1, parseInt(process.env.REVIEWERS_TO_REQUEST || "1", 10));

const gh = new Octokit({ auth: token });
const [owner, repo] = process.env.GITHUB_REPOSITORY.split("/");

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

function pickRandom(arr, k) {
  const list = [...arr];
  for (let i = list.length - 1; i > 0; i--) {
    const j = Math.floor(Math.random() * (i + 1));
    [list[i], list[j]] = [list[j], list[i]];
  }
  return list.slice(0, Math.max(0, Math.min(k, list.length)));
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

  if (!site) {
    console.log("Author not in configured teams; skipping assignee update.");
    return;
  }

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

  const needed = Math.max(0, n - assignedFromSite.length);

  const candidates = siteMembers.filter((member) => !alreadyAssigned.has(member));
  if (!candidates.length) {
    console.log(`All teammates from ${site} are already assigned; nothing to add.`);
    return;
  }

  const assignees = pickRandom(candidates, needed);
  if (!assignees.length) {
    console.log(`Unable to select additional assignees from ${site}; skipping.`);
    return;
  }

  await gh.issues.addAssignees({
    owner,
    repo,
    issue_number: num,
    assignees,
  });

  console.log(`Assigned ${assignees.join(", ")} to PR #${num}.`);
})().catch((error) => {
  console.error(error);
  process.exit(1);
});
