# How to Report a Translation Error

If you spot an incorrect, typo-ridden, or out-of-context translation in any of the `f4` language packs, we highly appreciate your help in correcting it!

To make reporting as easy and accurate as possible, please follow the steps below using our built-in **Translator Tool**.

---

## Step 1: Capture the Technical Information

You do not need to hunt through the source code or `.lng` files to find the internal key. The application can extract this automatically:

1. Open `f4` and navigate to the screen, dialog, or menu containing the incorrect string.
2. Hover your mouse cursor over the incorrect text (e.g., a button, checkbox, label, or menu item).
3. Hold **`Ctrl + Alt`** on your keyboard and **`Right-Click`** on that element.
4. `f4` will perform an instant reverse-lookup in the translation dictionary, build the UI hierarchy stack, and copy a technical report directly to your system clipboard.

Example of the copied clipboard text:
```text
--- f4 Translator Tool ---
Key:  PanelSettings.ShowHidden
Text: Show hidden and system files
Help Context: Panels -> Panel settings
```

---

## Step 2: Open a GitHub Issue

1. Go to the `f4` repository issues page: [https://github.com/unxed/f4/issues](https://github.com/unxed/f4/issues)
2. Click **New Issue**.
3. Use a clear title, for example: `Translation error: [Language Code] - [Brief Description]` (e.g., `Translation error: es - incorrect verb in settings`).
4. In the description box, paste the technical info you copied in Step 1.
5. Add your suggested correction using the following format:
   * **Current Text:** `[Paste the incorrect text currently shown in the UI]`
   * **Suggested Text:** `[Write the correct translation here]`
   * **Explanation/Context (Optional):** `[Explain why this correction is better or more natural]`

---

## Future Plans: In-App Error Reporting

We plan to automate this entire workflow directly inside the user interface to make localization improvements frictionless.

### Proposed Workflow
1. When a user holds **`Ctrl + Alt`** and **`Right-Clicks`** a UI element, a modal dialog box will appear.
2. The dialog will present two choices:
   * **[ Copy Info ]**: Copy the technical metadata to the clipboard (current behavior).
   * **[ Report Issue ]**: Open an in-app wizard to submit the correction directly.
3. The **Report Issue** wizard will:
   * Display the current translation and the technical key.
   * Provide an input field for the user to type the corrected translation.
   * Initiate a secure GitHub OAuth device flow to authorize the user (asking them to enter a short code on GitHub to authenticate).
   * Submit a structured GitHub Issue to our repository on behalf of the user's GitHub account.
4. **Automation on our end:** The issues created this way will follow a strict, machine-readable format (such as JSON or YAML metadata block). An automated backend script will parse these issues and automatically update the corresponding `.lng` files or open automated Pull Requests, minimizing human maintenance overhead.