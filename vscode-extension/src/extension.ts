import * as vscode from "vscode";
import * as path from "path";
import { BelayClient } from "./belayClient";
import { TimelineProvider } from "./timelineProvider";
import { SessionsProvider } from "./sessionsProvider";
import { BelayStatusBar } from "./statusBar";

let statusBar: BelayStatusBar | undefined;

export function activate(context: vscode.ExtensionContext): void {
  const client = new BelayClient(33412);

  // --- Tree view providers ---
  const timelineProvider = new TimelineProvider(client);
  const sessionsProvider = new SessionsProvider(client);

  context.subscriptions.push(
    vscode.window.registerTreeDataProvider("belay-timeline", timelineProvider),
    vscode.window.registerTreeDataProvider("belay-sessions", sessionsProvider)
  );

  // --- Status bar ---
  statusBar = new BelayStatusBar(client);
  statusBar.startPolling();
  context.subscriptions.push({ dispose: () => statusBar?.dispose() });

  // --- Commands ---

  // Refresh both views
  context.subscriptions.push(
    vscode.commands.registerCommand("belay.refresh", () => {
      timelineProvider.refresh();
      sessionsProvider.refresh();
    })
  );

  // Show file history via quick pick
  context.subscriptions.push(
    vscode.commands.registerCommand(
      "belay.showFileHistory",
      async (eventItemOrUri?: unknown) => {
        let filePath: string | undefined;

        // If invoked from tree context menu, extract the file path
        if (
          eventItemOrUri &&
          typeof eventItemOrUri === "object" &&
          "event" in (eventItemOrUri as Record<string, unknown>)
        ) {
          filePath = (
            (eventItemOrUri as Record<string, unknown>).event as {
              file_path: string;
            }
          ).file_path;
        }

        // If no path yet, prompt the user
        if (!filePath) {
          const activeEditor = vscode.window.activeTextEditor;
          const workspaceRoot =
            vscode.workspace.workspaceFolders?.[0]?.uri.fsPath;

          if (activeEditor && workspaceRoot) {
            filePath = path.relative(
              workspaceRoot,
              activeEditor.document.uri.fsPath
            );
          } else {
            filePath = await vscode.window.showInputBox({
              prompt: "Enter file path (relative to project root)",
              placeHolder: "src/main.ts",
            });
          }
        }

        if (!filePath) {
          return;
        }

        try {
          const events = await client.getFileHistory(filePath);
          if (events.length === 0) {
            vscode.window.showInformationMessage(
              `No Belay history found for ${filePath}`
            );
            return;
          }

          const items = events.map((e) => ({
            label: `${e.operation} at ${new Date(e.timestamp).toLocaleString()}`,
            description: e.session_id
              ? `Session: ${e.session_id.slice(0, 8)}...`
              : "No session",
            detail: e.content_hash
              ? `Hash: ${e.content_hash.slice(0, 16)}... (${e.content_size} bytes)`
              : "No content",
            event: e,
          }));

          const selected = await vscode.window.showQuickPick(items, {
            title: `Belay History: ${filePath}`,
            placeHolder: "Select a version to view",
          });

          if (selected?.event.content_hash) {
            try {
              const content = await client.getFileContent(
                selected.event.content_hash
              );
              const doc = await vscode.workspace.openTextDocument({
                content,
                language: getLanguageId(filePath),
              });
              await vscode.window.showTextDocument(doc, { preview: true });
            } catch (err) {
              vscode.window.showErrorMessage(
                `Failed to fetch file content: ${err}`
              );
            }
          }
        } catch (err) {
          vscode.window.showErrorMessage(
            `Failed to get file history: ${err}`
          );
        }
      }
    )
  );

  // Restore file to a specific version
  context.subscriptions.push(
    vscode.commands.registerCommand(
      "belay.restoreFile",
      async (eventItemOrUri?: unknown) => {
        let filePath: string | undefined;

        if (
          eventItemOrUri &&
          typeof eventItemOrUri === "object" &&
          "event" in (eventItemOrUri as Record<string, unknown>)
        ) {
          filePath = (
            (eventItemOrUri as Record<string, unknown>).event as {
              file_path: string;
            }
          ).file_path;
        }

        if (!filePath) {
          const activeEditor = vscode.window.activeTextEditor;
          const workspaceRoot =
            vscode.workspace.workspaceFolders?.[0]?.uri.fsPath;

          if (activeEditor && workspaceRoot) {
            filePath = path.relative(
              workspaceRoot,
              activeEditor.document.uri.fsPath
            );
          } else {
            filePath = await vscode.window.showInputBox({
              prompt: "Enter file path (relative to project root)",
              placeHolder: "src/main.ts",
            });
          }
        }

        if (!filePath) {
          return;
        }

        try {
          const events = await client.getFileHistory(filePath);
          const withContent = events.filter((e) => e.content_hash);

          if (withContent.length === 0) {
            vscode.window.showInformationMessage(
              `No restorable versions found for ${filePath}`
            );
            return;
          }

          const items = withContent.map((e) => ({
            label: `${e.operation} at ${new Date(e.timestamp).toLocaleString()}`,
            description: e.session_id
              ? `Session: ${e.session_id.slice(0, 8)}...`
              : "No session",
            detail: `${e.content_size} bytes -- hash: ${e.content_hash!.slice(0, 16)}...`,
            event: e,
          }));

          const selected = await vscode.window.showQuickPick(items, {
            title: `Restore ${filePath} to version`,
            placeHolder: "Select a version to restore",
          });

          if (!selected) {
            return;
          }

          const confirm = await vscode.window.showWarningMessage(
            `Restore ${filePath} to version from ${new Date(selected.event.timestamp).toLocaleString()}?`,
            { modal: true },
            "Restore"
          );

          if (confirm !== "Restore") {
            return;
          }

          const content = await client.getFileContent(
            selected.event.content_hash!
          );
          const workspaceRoot =
            vscode.workspace.workspaceFolders?.[0]?.uri.fsPath;
          if (!workspaceRoot) {
            vscode.window.showErrorMessage(
              "No workspace folder open to resolve file path"
            );
            return;
          }

          const fullPath = path.join(workspaceRoot, filePath);
          const fileUri = vscode.Uri.file(fullPath);
          const encoder = new TextEncoder();
          await vscode.workspace.fs.writeFile(fileUri, encoder.encode(content));

          vscode.window.showInformationMessage(
            `Restored ${filePath} to version from ${new Date(selected.event.timestamp).toLocaleString()}`
          );
        } catch (err) {
          vscode.window.showErrorMessage(
            `Failed to restore file: ${err}`
          );
        }
      }
    )
  );

  // Initial data load
  timelineProvider.refresh();
  sessionsProvider.refresh();

  vscode.window.showInformationMessage("Belay extension activated");
}

export function deactivate(): void {
  statusBar?.dispose();
  statusBar = undefined;
}

/**
 * Guess a VS Code language ID from a file path extension.
 */
function getLanguageId(filePath: string): string {
  const ext = path.extname(filePath).toLowerCase();
  const map: Record<string, string> = {
    ".ts": "typescript",
    ".tsx": "typescriptreact",
    ".js": "javascript",
    ".jsx": "javascriptreact",
    ".json": "json",
    ".py": "python",
    ".go": "go",
    ".rs": "rust",
    ".md": "markdown",
    ".css": "css",
    ".html": "html",
    ".yaml": "yaml",
    ".yml": "yaml",
    ".toml": "toml",
    ".sh": "shellscript",
    ".bash": "shellscript",
    ".sql": "sql",
    ".swift": "swift",
    ".gd": "gdscript",
  };
  return map[ext] ?? "plaintext";
}
