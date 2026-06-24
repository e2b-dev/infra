function versionFromPath(path) {
  const match = path.match(/^\/(v[0-9]+)(?:\/|$)/);
  return match?.[1];
}

function operationPath(ctx) {
  const location = ctx.location;
  const serializedLocation = typeof location?.toJSON === 'function'
    ? location.toJSON()
    : undefined;
  const pointer =
    location?.absolutePointer ??
    location?.pointer ??
    serializedLocation?.absolutePointer ??
    serializedLocation?.pointer ??
    location?.sourcePointer ??
    serializedLocation?.sourcePointer ??
    location?.pointerBase ??
    serializedLocation?.pointerBase;

  if (typeof pointer !== 'string') {
    return undefined;
  }

  const match = pointer.match(/#\/paths\/([^/]+)\//);
  if (!match) {
    return undefined;
  }

  const path = match[1].replace(/~1/g, '/').replace(/~0/g, '~');
  return path;
}

function VersionedOperationSummary() {
  return {
    Operation: {
      enter(operation, ctx) {
        const path = operationPath(ctx);
        if (!path) {
          return;
        }

        const version = versionFromPath(path);
        if (!version) {
          return;
        }

        const versionMarker = new RegExp(`\\(${version}(?:\\)|,)`);
        if (typeof operation.summary === 'string' && versionMarker.test(operation.summary)) {
          return;
        }

        ctx.report({
          message:
            'Versioned endpoint summaries must include the API version, for example "Create template (v3)".',
          location: operation.summary
            ? ctx.location.child('summary')
            : ctx.location,
        });
      },
    },
  };
}

function OperationSummaryNoGetPrefix() {
  return {
    Operation: {
      enter(operation, ctx) {
        if (typeof operation.summary !== 'string') {
          return;
        }

        if (!/^get\b/i.test(operation.summary)) {
          return;
        }

        ctx.report({
          message:
            'Operation summaries should not start with "Get"; the HTTP method is already shown.',
          location: ctx.location.child('summary'),
        });
      },
    },
  };
}

function OperationSummaryNoDeprecatedLabel() {
  return {
    Operation: {
      enter(operation, ctx) {
        if (typeof operation.summary !== 'string') {
          return;
        }

        if (!/\bdeprecated\b/i.test(operation.summary)) {
          return;
        }

        ctx.report({
          message:
            'Operation summaries should not include "deprecated"; use deprecated: true and the description instead.',
          location: ctx.location.child('summary'),
        });
      },
    },
  };
}

export default function e2bConsistencyPlugin() {
  return {
    id: 'e2b-consistency',
    rules: {
      oas3: {
        'versioned-operation-summary': VersionedOperationSummary,
        'operation-summary-no-get-prefix': OperationSummaryNoGetPrefix,
        'operation-summary-no-deprecated-label': OperationSummaryNoDeprecatedLabel,
      },
    },
  };
}
