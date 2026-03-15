import * as vscode from "vscode";
import { BelayClient, BelaySession, BelayEvent } from "./belayClient";

// --- Tree items ---

function getStatusIcon(status: string): vscode.ThemeIcon {
  switch (status) {
    case "active":
      return new vscode.ThemeIcon(
        "circle-filled",
        new vscode.ThemeColor("charts.green")
      );
    case "ended":
      return new vscode.ThemeIcon(
        "circle-outline",
        new vscode.ThemeColor("foreground")
      );
    case "crashed":
      return new vscode.ThemeIcon(
        "error",
        new vscode.ThemeColor("charts.red")
      );
    default:
      return new vscode.ThemeIcon("question");
  }
}

class SessionItem extends vscode.TreeItem {
  constructor(public readonly session: BelaySession) {
    const label = session.label || session.tool_name || session.session_id.slice(0, 12);
    super(label, vscode.TreeItemCollapsibleState.Collapsed);

    const statusLabel =
      session.status === "active" ? "Active" : session.status === "crashed" ? "Crashed" : "Ended";

    this.description = `${statusLabel} -- ${session.event_count} events -- ${session.duration}`;
    this.tooltip = new vscode.MarkdownString(
      [
        `**Session:** \`${session.session_id}\``,
        "",
        `Tool: ${session.tool_name}`,
        `Status: ${statusLabel}`,
        `Started: ${new Date(session.started_at).toLocaleString()}`,
        session.ended_at
          ? `Ended: ${new Date(session.ended_at).toLocaleString()}`
          : "",
        `Duration: ${session.duration}`,
        `Files changed: ${session.files_changed}`,
        `Events: ${session.event_count}`,
      ]
        .filter(Boolean)
        .join("\n")
    );
    this.iconPath = getStatusIcon(session.status);
    this.contextValue = "session";
  }
}

class SessionFileItem extends vscode.TreeItem {
  constructor(
    public readonly event: BelayEvent,
    workspaceRoot: string | undefined
  ) {
    const fileName = event.file_path.split("/").pop() ?? event.file_path;
    super(fileName, vscode.TreeItemCollapsibleState.None);

    this.description = `${event.operation}`;
    this.tooltip = `${event.file_path} -- ${event.operation} at ${new Date(event.timestamp).toLocaleString()}`;
    this.contextValue = "sessionFile";

    // Click opens the file
    if (event.operation !== "DELETE") {
      const filePath = workspaceRoot
        ? `${workspaceRoot}/${event.file_path}`
        : event.file_path;
      this.command = {
        command: "vscode.open",
        title: "Open File",
        arguments: [vscode.Uri.file(filePath)],
      };
    }

    switch (event.operation) {
      case "CREATE":
        this.iconPath = new vscode.ThemeIcon(
          "diff-added",
          new vscode.ThemeColor("charts.green")
        );
        break;
      case "MODIFY":
        this.iconPath = new vscode.ThemeIcon(
          "diff-modified",
          new vscode.ThemeColor("charts.blue")
        );
        break;
      case "DELETE":
        this.iconPath = new vscode.ThemeIcon(
          "diff-removed",
          new vscode.ThemeColor("charts.red")
        );
        break;
      case "RENAME":
        this.iconPath = new vscode.ThemeIcon(
          "diff-renamed",
          new vscode.ThemeColor("charts.yellow")
        );
        break;
    }
  }
}

// --- Provider ---

export class SessionsProvider
  implements vscode.TreeDataProvider<SessionItem | SessionFileItem>
{
  private _onDidChangeTreeData = new vscode.EventEmitter<
    SessionItem | SessionFileItem | undefined | null | void
  >();
  readonly onDidChangeTreeData = this._onDidChangeTreeData.event;

  private sessions: BelaySession[] = [];
  private sessionEventsCache = new Map<string, BelayEvent[]>();
  private workspaceRoot: string | undefined;

  constructor(private client: BelayClient) {
    const folders = vscode.workspace.workspaceFolders;
    if (folders && folders.length > 0) {
      this.workspaceRoot = folders[0].uri.fsPath;
    }
  }

  refresh(): void {
    this.sessionEventsCache.clear();
    this.loadSessions();
  }

  private async loadSessions(): Promise<void> {
    try {
      this.sessions = await this.client.getSessions(20);
    } catch {
      this.sessions = [];
    }
    this._onDidChangeTreeData.fire();
  }

  getTreeItem(element: SessionItem | SessionFileItem): vscode.TreeItem {
    return element;
  }

  async getChildren(
    element?: SessionItem | SessionFileItem
  ): Promise<(SessionItem | SessionFileItem)[]> {
    if (!element) {
      // Root level: show sessions
      return this.sessions.map((s) => new SessionItem(s));
    }

    if (element instanceof SessionItem) {
      // Expand session to show its files
      const sessionId = element.session.session_id;

      if (!this.sessionEventsCache.has(sessionId)) {
        try {
          const events = await this.client.getSessionEvents(sessionId);
          // Deduplicate by file_path, keeping latest event per file
          const fileMap = new Map<string, BelayEvent>();
          for (const event of events) {
            fileMap.set(event.file_path, event);
          }
          this.sessionEventsCache.set(
            sessionId,
            Array.from(fileMap.values())
          );
        } catch {
          this.sessionEventsCache.set(sessionId, []);
        }
      }

      const events = this.sessionEventsCache.get(sessionId) ?? [];
      return events.map((e) => new SessionFileItem(e, this.workspaceRoot));
    }

    return [];
  }
}
