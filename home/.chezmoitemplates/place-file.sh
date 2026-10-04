# Copy the file $1 to $2 so that $2 only ever holds a complete file: copy it
# beside $2 under another name, then rename it into place. A step that checks
# for $2 therefore never skips a file a stopped copy left half written.
# Included below a script's probe block, since it writes.
place_file() {
    cp "$1" "$2.part" && mv -f "$2.part" "$2" || { rm -f "$2.part"; return 1; }
}
