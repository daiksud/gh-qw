import { defineMdastPlugin, type MdastNode } from "satteri";

type Blockquote = Extract<MdastNode, { type: "blockquote" }>;
type Paragraph = Extract<MdastNode, { type: "paragraph" }>;
type Text = Extract<MdastNode, { type: "text" }>;
type AlertType = "NOTE" | "TIP" | "IMPORTANT" | "WARNING" | "CAUTION";

interface AlertDefinition {
  readonly label: string;
  readonly slug: Lowercase<AlertType>;
  readonly iconName: string;
  readonly iconPath: string;
}

// Paths are from @primer/octicons 19.29.2. See OCTICONS_LICENSE.
const ALERTS: Readonly<Record<AlertType, AlertDefinition>> = {
  NOTE: {
    label: "Note",
    slug: "note",
    iconName: "info",
    iconPath:
      "M0 8a8 8 0 1 1 16 0A8 8 0 0 1 0 8Zm8-6.5a6.5 6.5 0 1 0 0 13 6.5 6.5 0 0 0 0-13ZM6.5 7.75A.75.75 0 0 1 7.25 7h1a.75.75 0 0 1 .75.75v2.75h.25a.75.75 0 0 1 0 1.5h-2a.75.75 0 0 1 0-1.5h.25v-2h-.25a.75.75 0 0 1-.75-.75ZM8 6a1 1 0 1 1 0-2 1 1 0 0 1 0 2Z",
  },
  TIP: {
    label: "Tip",
    slug: "tip",
    iconName: "light-bulb",
    iconPath:
      "M8 1.5c-2.363 0-4 1.69-4 3.75 0 .984.424 1.625.984 2.304l.214.253c.223.264.47.556.673.848.284.411.537.896.621 1.49a.75.75 0 0 1-1.484.211c-.04-.282-.163-.547-.37-.847a8.456 8.456 0 0 0-.542-.68c-.084-.1-.173-.205-.268-.32C3.201 7.75 2.5 6.766 2.5 5.25 2.5 2.31 4.863 0 8 0s5.5 2.31 5.5 5.25c0 1.516-.701 2.5-1.328 3.259-.095.115-.184.22-.268.319-.207.245-.383.453-.541.681-.208.3-.33.565-.37.847a.751.751 0 0 1-1.485-.212c.084-.593.337-1.078.621-1.489.203-.292.45-.584.673-.848.075-.088.147-.173.213-.253.561-.679.985-1.32.985-2.304 0-2.06-1.637-3.75-4-3.75ZM5.75 12h4.5a.75.75 0 0 1 0 1.5h-4.5a.75.75 0 0 1 0-1.5ZM6 15.25a.75.75 0 0 1 .75-.75h2.5a.75.75 0 0 1 0 1.5h-2.5a.75.75 0 0 1-.75-.75Z",
  },
  IMPORTANT: {
    label: "Important",
    slug: "important",
    iconName: "report",
    iconPath:
      "M0 1.75C0 .784.784 0 1.75 0h12.5C15.216 0 16 .784 16 1.75v9.5A1.75 1.75 0 0 1 14.25 13H8.06l-2.573 2.573A1.458 1.458 0 0 1 3 14.543V13H1.75A1.75 1.75 0 0 1 0 11.25Zm1.75-.25a.25.25 0 0 0-.25.25v9.5c0 .138.112.25.25.25h2a.75.75 0 0 1 .75.75v2.19l2.72-2.72a.749.749 0 0 1 .53-.22h6.5a.25.25 0 0 0 .25-.25v-9.5a.25.25 0 0 0-.25-.25Zm7 2.25v2.5a.75.75 0 0 1-1.5 0v-2.5a.75.75 0 0 1 1.5 0ZM9 9a1 1 0 1 1-2 0 1 1 0 0 1 2 0Z",
  },
  WARNING: {
    label: "Warning",
    slug: "warning",
    iconName: "alert",
    iconPath:
      "M6.457 1.047c.659-1.234 2.427-1.234 3.086 0l6.082 11.378A1.75 1.75 0 0 1 14.082 15H1.918a1.75 1.75 0 0 1-1.543-2.575Zm1.763.707a.25.25 0 0 0-.44 0L1.698 13.132a.25.25 0 0 0 .22.368h12.164a.25.25 0 0 0 .22-.368Zm.53 3.996v2.5a.75.75 0 0 1-1.5 0v-2.5a.75.75 0 0 1 1.5 0ZM9 11a1 1 0 1 1-2 0 1 1 0 0 1 2 0Z",
  },
  CAUTION: {
    label: "Caution",
    slug: "caution",
    iconName: "stop",
    iconPath:
      "M4.47.22A.749.749 0 0 1 5 0h6c.199 0 .389.079.53.22l4.25 4.25c.141.14.22.331.22.53v6a.749.749 0 0 1-.22.53l-4.25 4.25A.749.749 0 0 1 11 16H5a.749.749 0 0 1-.53-.22L.22 11.53A.749.749 0 0 1 0 11V5c0-.199.079-.389.22-.53Zm.84 1.28L1.5 5.31v5.38l3.81 3.81h5.38l3.81-3.81V5.31L10.69 1.5ZM8 4a.75.75 0 0 1 .75.75v3.5a.75.75 0 0 1-1.5 0v-3.5A.75.75 0 0 1 8 4Zm0 8a1 1 0 1 1 0-2 1 1 0 0 1 0 2Z",
  },
};

const ALERT_MARKER = /^\[!(NOTE|TIP|IMPORTANT|WARNING|CAUTION)\][\t ]*(?:\r?\n|$)/i;

function iconSvg(definition: AlertDefinition): string {
  return `<svg class="octicon octicon-${definition.iconName} mr-2" viewBox="0 0 16 16" width="16" height="16" aria-hidden="true"><path d="${definition.iconPath}"></path></svg>`;
}

function titleParagraph(definition: AlertDefinition): Paragraph {
  return {
    type: "paragraph",
    data: {
      hProperties: {
        className: ["markdown-alert-title"],
        dir: "auto",
      },
    },
    children: [
      { type: "html", value: iconSvg(definition) },
      { type: "text", value: definition.label },
    ],
  };
}

function isMarkerOnOwnLine(
  marker: Text,
  markerMatch: RegExpExecArray,
  remainingInline: Paragraph["children"],
  hasFollowingBlock: boolean,
): boolean {
  if (markerMatch[0].includes("\n") || markerMatch[0].includes("\r")) {
    return true;
  }

  const next = remainingInline[0];
  if (next?.type === "break") {
    return true;
  }

  if (next?.position?.start.line && marker.position?.end.line) {
    return next.position.start.line > marker.position.end.line;
  }

  return remainingInline.length === 0 && hasFollowingBlock;
}

function alertBody(node: Readonly<Blockquote>): {
  readonly type: AlertType;
  readonly children: Blockquote["children"];
} | null {
  const firstBlock = node.children[0];
  if (firstBlock?.type !== "paragraph") {
    return null;
  }

  const firstInline = firstBlock.children[0];
  if (firstInline?.type !== "text") {
    return null;
  }

  const markerMatch = ALERT_MARKER.exec(firstInline.value);
  if (!markerMatch) {
    return null;
  }

  const remainingInline = [...firstBlock.children];
  const textAfterMarker = firstInline.value.slice(markerMatch[0].length);
  if (textAfterMarker.length > 0) {
    remainingInline[0] = { ...firstInline, value: textAfterMarker };
  } else {
    remainingInline.shift();
  }

  if (
    !isMarkerOnOwnLine(
      firstInline,
      markerMatch,
      remainingInline,
      node.children.length > 1,
    )
  ) {
    return null;
  }

  // Two spaces after the marker create a hard-break node. It belongs to the
  // marker line and must not become a leading <br> in the alert body.
  if (textAfterMarker.length === 0 && remainingInline[0]?.type === "break") {
    remainingInline.shift();
  }

  const body = [...node.children.slice(1)];
  if (remainingInline.length > 0) {
    body.unshift({ ...firstBlock, children: remainingInline });
  }

  // GitHub only renders an alert when the marker is followed by content.
  if (body.length === 0) {
    return null;
  }

  return {
    type: markerMatch[1]!.toUpperCase() as AlertType,
    children: body,
  };
}

/**
 * Convert top-level GitHub alert blockquotes to GitHub-compatible markup.
 *
 * The plugin deliberately ignores nested alerts and malformed markers, so
 * ordinary blockquote behavior remains intact.
 */
export function githubAlerts() {
  return defineMdastPlugin({
    name: "github-alerts",
    blockquote(node, context) {
      if (context.parent(node)?.type !== "root") {
        return;
      }

      const parsed = alertBody(node);
      if (!parsed) {
        return;
      }

      const definition = ALERTS[parsed.type];
      return {
        type: "blockquote",
        position: node.position,
        data: {
          hName: "div",
          hProperties: {
            className: [
              "markdown-alert",
              `markdown-alert-${definition.slug}`,
            ],
            dir: "auto",
          },
        },
        children: [titleParagraph(definition), ...parsed.children],
      };
    },
  });
}

export default githubAlerts;
