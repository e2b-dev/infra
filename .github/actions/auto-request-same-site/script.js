const { Octokit } = require("@octokit/rest");
const fs = require("fs");

const token = process.env.APP_TOKEN;         // GitHub App installation token
const org   = process.env.ORG;
const sf    = process.env.SF_TEAM_SLUG;
const prg   = process.env.PRG_TEAM_SLUG;
const n     = parseInt(process.env.REVIEWERS_TO_REQUEST || "1", 10);

const gh = new Octokit({ auth: token });
const [owner, repo] = process.env.GITHUB_REPOSITORY.split("/");

function prNumber() {
  const ev = JSON.parse(fs.readFileSync(process.env.GITHUB_EVENT_PATH, "utf8"));
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

(async () => {
  const num = prNumber();
  if (!num) { console.log("No PR number; exiting."); return; }

  const pr = await getPR(num);
  const author = pr.user.login;

  const teams = await getUserTeams(author);
  const site = teams.includes(sf) ? sf : teams.includes(prg) ? prg : null;
  if (!site) { console.log("Author not in eng-sf or eng-prg; skipping."); return; }

  // Individuals (default)
  const members = (await listTeamMembers(site)).filter(u => u !== author);
  const already = new Set(pr.requested_reviewers.map(r => r.login));
  const candidates = members.filter(m => !already.has(m));
  const reviewers = pickRandom(candidates, n);

  if (reviewers.length) {
    await gh.pulls.requestReviewers({ owner, repo, pull_number: num, reviewers });
    console.log(`Requested ${reviewers.join(", ")} from ${site}`);
  } else {
    console.log(`No candidates to request from ${site}.`);
  }

  // Or request the whole team:
  // await gh.pulls.requestReviewers({ owner, repo, pull_number: num, team_reviewers: [site] });
  // console.log(`Requested team ${site}`);
})().catch(e => { console.error(e); process.exit(1); });
