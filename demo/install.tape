# Fabrika install demo — record with:  vhs demo/install.tape
# Produces demo/install.gif (real installer, no sudo via FABRIKA_INSTALL_DIR).

Output demo/install.gif

Require curl
Require git

Set Shell "bash"
Set FontFamily "Menlo"
Set FontSize 18
Set Width 1200
Set Height 680
Set Padding 26
Set Theme "Catppuccin Mocha"
Set TypingSpeed 35ms
Set WindowBar Colorful
Set WindowBarSize 38
Set BorderRadius 10
Set Margin 16
Set MarginFill "#1e1e2e"

# --- hidden setup: clean state, tidy prompt, a believable sample repo ---
Hide
Type "export PS1='$ '" Enter
Type "rm -f ~/.local/bin/fabrika; mkdir -p ~/.local/bin" Enter
Type 'export PATH="$HOME/.local/bin:$PATH"' Enter
Type "rm -rf ~/fabrika-demo && mkdir -p ~/fabrika-demo/src && cd ~/fabrika-demo && git init -q && printf '# my-app\n' > README.md && printf 'export const x = 1\n' > src/index.ts && git add -A && git -c user.email=demo@fab -c user.name=demo commit -q -m init && cd ~" Enter
Type "clear" Enter
Show

# --- 1. install it (the real GitHub release) ---
Type "curl -fsSL https://raw.githubusercontent.com/berkaycubuk/fabrika/main/install.sh | FABRIKA_INSTALL_DIR=$HOME/.local/bin sh"
Sleep 700ms
Enter
Sleep 5s

# --- 2. it's just a binary ---
Type "fabrika version"
Sleep 400ms
Enter
Sleep 1500ms

# --- 3. point it at any repo ---
Type "cd ~/fabrika-demo"
Enter
Sleep 700ms
Type "fabrika init"
Sleep 400ms
Enter
Sleep 2s

# --- 4. launch the board — hold on the "serving" line as the payoff ---
Type "fabrika -no-open"
Sleep 400ms
Enter
Sleep 5s
