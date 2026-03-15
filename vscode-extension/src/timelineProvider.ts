import * as vscode from "vscode";
import { BelayClient, BelayEvent } from "./belayClient";

// --- Time group helpers ---

type TimeGroup = "Last Hour" | "Today" | "Yesterday" | "Older";

function getTimeGroup(timestamp: string): TimeGroup {
  const eventTime = new Date(timestamp);
  const now = new Date();

  const diffMs = now.getTime() - eventTime.getTime();
  const oneHour = 60 * 60 * 1000;
  const today = new Date(now.getFullYear(), now.getMonth(), now.getDate());
  const yesterday = new Date(today.getTime() - 24 * 60 * 60 * 1000);

  if (diffMs < oneHour) {
    return "Last Hour";
  } else if (eventTime >= today) {
    return "Today";
  } else if (eventTime >= yesterday) {
    return "Yesterday";
  } else {
    return "Older";
  }
}

function getOperationIcon(op: string): vscode.ThemeIcon {
  switch (op) {
    case "CREATE":
      return new vscode.ThemeIcon(
        "diff-added",
        new vscode.ThemeColor("charts.green")
      );
    case "MODIFY":
      return new vscode.ThemeIcon(
        "diff-modified",
        new vscode.ThemeColor("charts.blue")
      );
    case "DELETE":
      return new vscode.ThemeIcon(
        "diff-removed",
        new vscode.ThemeColor("charts.red")
      );
    case "RENAME":
      return new vscode.ThemeIcon(
        "diff-renamed",
        new vscode.ThemeColor("charts.yellow")
      );
    default:
      return new vscode.ThemeIcon("circle-outline");
  }
}

function formatTime(timestamp: string): string {
  const d = new Date(timestamp);
  return d.toLocaleTimeString(undefined, {
    hour: "2-digit",
    minute: "2-digit",
  });
}

// --- Tree items ---

class TimeGroupItem extends vscode.TreeItem {
  constructor(
    public readonly group: TimeGroup,
    public readonly events: BelayEvent[]
  ) {
    super(group, vscode.TreeItemCollapsibleState.Expanded);
    this.description = `${events.length} change${events.length !== 1 ? "s" : ""}`;
    this.contextValue = "timeGroup";
  }
}

class EventItem extends vscode.TreeItem {
  constructor(public readonly event: BelayEvent) {
    const fileName = event.file_path.split("/").pop() ?? event.file_path;
    super(fileName, vscode.TreeItemCollapsibleState.None);

    const sessionLabel = event.session_id
      ? ` [${event.session_id.slice(0, 8)}]`
      : "";
    this.description = `${event.operation} ${formatTime(event.timestamp)}${sessionLabel}`;
    this.tooltip = new vscode.MarkdownString(
      [
        `**${event.file_path}**`,
        "",
        `Operation: \`${event.operation}\``,
        `Time: ${new Date(event.timestamp).toLocaleString()}`,
        event.session_id ? `Session: \`${event.session_id}\`` : "",
        event.content_hash
          ? `Hash: \`${event.content_hash.slice(0, 12)}...\``
          : "",
        `Size: ${event.content_size} bytes`,
      ]
        .filter(Boolean)
        .join("\n")
    );
    this.iconPath = getOperationIcon(event.operation);
    this.contextValue = "event";

    // Click opens the file
    if (event.operation !== "DELETE") {
      this.command = {
        command: "vscode.open",
        title: "Open File",
        arguments: [vscode.Uri.file(event.file_path)],
      };
    }
  }
}

// --- Provider ---

export class TimelineProvider
  implements vscode.TreeDataProvider<TimeGroupItem | EventItem>
{
  private _onDidChangeTreeData = new vscode.EventEmitter<
    TimeGroupItem | EventItem | undefined | null | void
  >();
  readonly onDidChangeTreeData = this._onDidChangeTreeData.event;

  private events: BelayEvent[] = [];
  private workspaceRoot: string | undefined;

  constructor(private client: BelayClient) {
    const folders = vscode.workspace.workspaceFolders;
    if (folders && folders.length > 0) {
      this.workspaceRoot = folders[0].uri.fsPath;
    }
  }

  refresh(): void {
    this.loadEvents();
  }

  private async loadEvents(): Promise<void> {
    try {
      this.events = await this.client.getEvents(50);

      // If we have a workspace root, try to resolve relative paths
      if (this.workspaceRoot) {
        // Events use relative paths from the Belay project root.
        // We keep them as-is for display but resolve for opening.
      }
    } catch {
      this.events = [];
    }
    this._onDidChangeTreeData.fire();
  }

  getTreeItem(
    element: TimeGroupItem | EventItem
  ): vscode.TreeItem {
    return element;
  }

  getChildren(
    element?: TimeGroupItem | EventItem
  ): (TimeGroupItem | EventItem)[] {
    if (!element) {
      // Root level: group events by time
      const groups = new Map<TimeGroup, BelayEvent[]>();
      const order: TimeGroup[] = [
        "Last Hour",
        "Today",
        "Yesterday",
        "Older",
      ];

      for (const group of order) {
        groups.set(group, []);
      }

      for (const event of this.events) {
        const group = getTimeGroup(event.timestamp);
        groups.get(group)!.push(event);
      }

      // Only return groups that have events
      return order
        .filter((g) => groups.get(g)!.length > 0)
        .map((g) => new TimeGroupItem(g, groups.get(g)!));
    }

    if (element instanceof TimeGroupItem) {
      return element.events.map((e) => {
        const item = new EventItem(e);
        // Resolve file path relative to workspace
        if (this.workspaceRoot && e.operation !== "DELETE") {
          item.command = {
            command: "vscode.open",
            title: "Open File",
            arguments: [
              vscode.Uri.file(`${this.workspaceRoot}/${e.file_path}`),
            ],
          };
        }
        return item;
      });
    }

    return [];
  }
}
