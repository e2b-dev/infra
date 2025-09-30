const { Octokit } = require("@octokit/rest");
const fs = require("fs");

const token = process.env.APP_TOKEN; // GitHub App installation token
const org   = process.env.ORG;
const sf    = process.env.SF_TEAM_SLUG;
const prg   = process.env.PRG_TEAM_SLUG;
const n     = parseInt(process.env.REVIEWERS_TO_REQUEST || "1", 10);
const TEAM_MODE = (process.env.TEAM_MODE || "false").toLowerCase() === "true";

const gh = new Octokit({ auth: token });
const [owner, repo] = process.env.GITHUB_REPOSITORY.split("/");

// ---- helpers ----
function getEvent() {
  return JSON.parse(fs.readFileSync(process.env.GITHUB_EVENT_PATH, "utf8"));
}
function prNumber(ev) {
  return ev.pull_request?.number || null;
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
  return (data.organization?.teams?.nodes || []).map(t => t.slug);
}
async function listTeamMembers(teamSlug) {
  const res = await gh.teams.listMembersInOrg({ org, team_slug: teamSlug, per_page: 100 });
  return res.data.map(u => u.login);
}
function pickRandom(arr, k) {
  const a = [...arr];
  for (let i = a.length - 1; i > 0; i--) {
    const j = Math.floor(Math.random() * (i + 1));
    [a[i], a[j]] = [a[j], a[i]];
  }
  return a.slice(0, Math.max(0, Math.min(k, a.length)));
}

// Read CODEOWNERS (from usual locations) and parse the global "*" owners.
// Returns { users: [logins], teams: [teamSlugs] }
async function readDefaultCodeownersOwners(baseRef) {
  const paths = [".github/CODEOWNERS", "CODEOWNERS"];
  let text = "";
  for (const path of paths) {
    try {
      const { data } = await gh.repos.getContent({ owner, repo, path, ref: baseRef });
      if (Array.isArray(data)) continue; // directory
      text = Buffer.from(data.content, "base64").toString("utf8");
      break;
    } catch { /* try next */ }
  }
  if (!text) return { users: [], teams: [] };

  // find last matching "*" rule (later rules take precedence)
  const lines = text.split(/\r?\n/);
  let starOwners = null;
  for (const raw of lines) {
    const line = raw.trim();
    if (!line || line.startsWith("#")) continue;
    const parts = line.split(/\s+/);
    if (parts[0] === "*") starOwners = parts.slice(1);
  }
  if (!starOwners) return { users: [], teams: [] };

  const users = [];
  const teams = [];
  for (const ownerRef of starOwners) {
    if (!ownerRef.startsWith("@")) continue;
    const clean = ownerRef.slice(1); // remove leading '@'
    const slash = clean.indexOf("/");
    if (slash > -1) {
      // looks like org/team
      const maybeOrg = clean.slice(0, slash);
      const teamSlug = clean.slice(slash + 1);
      if (maybeOrg.toLowerCase() === org.toLowerCase()) teams.push(teamSlug);
      // if CODEOWNERS references another org's team, we ignore
    } else {
      users.push(clean);
    }
  }
  return { users, teams };
}

(async () => {
  const ev = getEvent();
  const num = prNumber(ev);
  if (!num) { console.log("No PR number; exiting."); return; }

  const baseRef = ev.pull_request?.base?.ref || "main";
  const pr = await getPR(num);
  const author = pr.user.login;

  // Determine author site
  const teams = await getUserTeams(author);
  const site = teams.includes(sf) ? sf : teams.includes(prg) ? prg : null;

  // If author is not in eng-sf or eng-prg => do nothing (keep CODEOWNERS defaults)
  if (!site) {
    console.log("Author not in eng-sf or eng-prg; keeping CODEOWNERS reviewers.");
    return;
  }

  // Author IS internal: remove global CODEOWNERS defaults, then assign same-site reviewers
  const defaults = await readDefaultCodeownersOwners(baseRef);
  console.log("CODEOWNERS * defaults:", defaults);

  // Find currently requested users & teams
  const { data: req } = await gh.pulls.listRequestedReviewers({ owner, repo, pull_number: num });
  const currentUsers = new Set(req.users.map(u => u.login));
  const currentTeams = new Set(req.teams.map(t => t.slug));

  // Compute removal sets based on CODEOWNERS defaults
  const toRemoveUsers = defaults.users.filter(u => currentUsers.has(u));
  const toRemoveTeams = defaults.teams.filter(t => currentTeams.has(t));

  if (toRemoveUsers.length || toRemoveTeams.length) {
    await gh.pulls.removeRequestedReviewers({
      owner, repo, pull_number: num,
      reviewers: toRemoveUsers,
      team_reviewers: toRemoveTeams
    });
    console.log(
      "Removed CODEOWNERS defaults:",
      toRemoveUsers.length ? `users=[${toRemoveUsers.join(", ")}]` : "users=[]",
      toRemoveTeams.length ? `teams=[${toRemoveTeams.join(", ")}]` : "teams=[]"
    );
  } else {
    console.log("No CODEOWNERS defaults currently requested (nothing to remove).");
  }

  // Now request same-site reviewers
  if (TEAM_MODE) {
    await gh.pulls.requestReviewers({ owner, repo, pull_number: num, team_reviewers: [site] });
    console.log(`Requested team ${site}`);
    return;
  }

  const siteMembers = (await listTeamMembers(site)).filter(u => u !== author);
  // refresh requested reviewers after potential removals
  const { data: req2 } = await gh.pulls.listRequestedReviewers({ owner, repo, pull_number: num });
  const already = new Set(req2.users.map(u => u.login));
  const candidates = siteMembers.filter(m => !already.has(m));
  const reviewers = pickRandom(candidates, n);

  if (reviewers.length) {
    await gh.pulls.requestReviewers({ owner, repo, pull_number: num, reviewers });
    console.log(`Requested ${reviewers.join(", ")} from ${site}`);
  } else {
    console.log(`No candidates to request from ${site}.`);
  }
})().catch(e => { console.error(e); process.exit(1); });
