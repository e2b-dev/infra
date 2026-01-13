const { Octokit } = require("@octokit/rest");
const { WebClient } = require("@slack/web-api");

/**
 * Creates a SlackUtils instance for GitHub-to-Slack user lookup and messaging.
 *
 * @param {Object} options
 * @param {string} options.githubToken - GitHub token with read:org and read:user permissions
 * @param {string} options.slackBotToken - Slack Bot token with users:read.email and chat:write permissions
 * @param {string} options.org - GitHub organization name (e.g., 'e2b-dev')
 * @param {string} [options.emailDomain] - Required email domain (e.g., 'e2b.dev'). If set, only users with this domain will be looked up.
 */
function createSlackUtils({ githubToken, slackBotToken, org, emailDomain = "e2b.dev" }) {
  const gh = new Octokit({ auth: githubToken });
  const slack = slackBotToken ? new WebClient(slackBotToken) : null;

  // Cache for GitHub username -> Slack user ID lookups
  const slackUserCache = new Map();

  /**
   * Get organization-verified email for a GitHub user.
   */
  async function getOrgVerifiedEmail(username) {
    try {
      const data = await gh.graphql(
        `query($org:String!, $login:String!){
          user(login:$login){
            organizationVerifiedDomainEmails(login:$org)
          }
        }`,
        { org, login: username }
      );

      const emails = data.user?.organizationVerifiedDomainEmails || [];
      const verified = emailDomain ? emails.find((email) => email.endsWith(`@${emailDomain}`)) : emails[0];

      if (verified) {
        console.log(`Found org-verified email ${verified} for ${username}`);
      }
      return verified || null;
    } catch (error) {
      console.log(`GraphQL org email lookup failed for ${username}: ${error.message}`);
      return null;
    }
  }

  /**
   * Get email for a GitHub user (tries org-verified first, then public profile).
   */
  async function getGitHubUserEmail(username) {
    const orgEmail = await getOrgVerifiedEmail(username);
    if (orgEmail) {
      return orgEmail;
    }

    try {
      const { data } = await gh.users.getByUsername({ username });
      const email = data.email || null;

      if (email && emailDomain && !email.endsWith(`@${emailDomain}`)) {
        console.log(`Public email ${email} for ${username} doesn't match required domain @${emailDomain}`);
        return null;
      }

      return email;
    } catch (error) {
      console.log(`Failed to fetch email for ${username}: ${error.message}`);
      return null;
    }
  }

  /**
   * Get Slack user ID for a GitHub username.
   * Uses email-based lookup via Slack API.
   *
   * @param {string} githubUsername
   * @returns {Promise<string|null>} Slack user ID or null if not found
   */
  async function getSlackUserId(githubUsername) {
    if (!slack) {
      console.log("Slack not configured; cannot look up user.");
      return null;
    }

    // Check cache first
    if (slackUserCache.has(githubUsername)) {
      return slackUserCache.get(githubUsername);
    }

    const email = await getGitHubUserEmail(githubUsername);
    if (!email) {
      console.log(`No valid email found for ${githubUsername}`);
      slackUserCache.set(githubUsername, null);
      return null;
    }

    try {
      const result = await slack.users.lookupByEmail({ email });
      if (result.ok && result.user?.id) {
        console.log(`Found Slack user ${result.user.id} for ${githubUsername} via ${email}`);
        slackUserCache.set(githubUsername, result.user.id);
        return result.user.id;
      }
    } catch (error) {
      console.log(`Slack lookup failed for ${githubUsername} (${email}): ${error.message}`);
    }

    slackUserCache.set(githubUsername, null);
    return null;
  }

  /**
   * Send a direct message to a Slack user.
   *
   * @param {string} slackUserId - Slack user ID
   * @param {string} text - Plain text message (used as fallback)
   * @param {Array} [blocks] - Slack Block Kit blocks for rich formatting
   * @returns {Promise<boolean>} True if message was sent successfully
   */
  async function sendDirectMessage(slackUserId, text, blocks = null) {
    if (!slack) {
      console.log("Slack not configured; cannot send message.");
      return false;
    }

    try {
      const payload = {
        channel: slackUserId,
        text,
      };

      if (blocks) {
        payload.blocks = blocks;
      }

      await slack.chat.postMessage(payload);
      console.log(`Sent Slack DM to ${slackUserId}`);
      return true;
    } catch (error) {
      console.log(`Failed to send Slack DM to ${slackUserId}: ${error.message}`);
      return false;
    }
  }

  /**
   * Send a direct message to a GitHub user via Slack.
   *
   * @param {string} githubUsername
   * @param {string} text - Plain text message
   * @param {Array} [blocks] - Slack Block Kit blocks
   * @returns {Promise<boolean>} True if message was sent successfully
   */
  async function sendDirectMessageToGitHubUser(githubUsername, text, blocks = null) {
    const slackUserId = await getSlackUserId(githubUsername);
    if (!slackUserId) {
      console.log(`Cannot send DM to ${githubUsername}: no Slack user found`);
      return false;
    }

    return sendDirectMessage(slackUserId, text, blocks);
  }

  /**
   * Send a message to a Slack channel.
   *
   * @param {string} channel - Channel ID or name
   * @param {string} text - Plain text message
   * @param {Array} [blocks] - Slack Block Kit blocks
   * @returns {Promise<boolean>} True if message was sent successfully
   */
  async function sendChannelMessage(channel, text, blocks = null) {
    if (!slack) {
      console.log("Slack not configured; cannot send message.");
      return false;
    }

    try {
      const payload = { channel, text };
      if (blocks) {
        payload.blocks = blocks;
      }

      await slack.chat.postMessage(payload);
      console.log(`Sent message to channel ${channel}`);
      return true;
    } catch (error) {
      console.log(`Failed to send message to channel ${channel}: ${error.message}`);
      return false;
    }
  }

  /**
   * Format a Slack user mention. If Slack user ID is available, returns <@USERID>,
   * otherwise returns the GitHub username as plain text.
   *
   * @param {string} githubUsername
   * @returns {Promise<string>} Slack mention or plain username
   */
  async function formatUserMention(githubUsername) {
    const slackUserId = await getSlackUserId(githubUsername);
    if (slackUserId) {
      return `<@${slackUserId}>`;
    }
    return githubUsername;
  }

  /**
   * Check if Slack is configured and available.
   */
  function isSlackConfigured() {
    return slack !== null;
  }

  return {
    getSlackUserId,
    getGitHubUserEmail,
    sendDirectMessage,
    sendDirectMessageToGitHubUser,
    sendChannelMessage,
    formatUserMention,
    isSlackConfigured,
  };
}

module.exports = { createSlackUtils };
