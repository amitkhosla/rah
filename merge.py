import os

def merge_files_recursive():
    current_script = os.path.basename(__file__)
    all_files = []

    # Step 1: Scan folders and subfolders
    for root, dirs, files in os.walk('.'):
        for file in files:
             # Skip unwanted file patterns
            if file.endswith('.md') or file.endswith('_test.go'):
                continue
            # Get the relative path (e.g., "logs/temp/data.txt")
            rel_path = os.path.relpath(os.path.join(root, file), '.')
            
            # Skip the script itself and the output file
            if rel_path != current_script and rel_path != "merged_output.txt":
                all_files.append(rel_path)

    if not all_files:
        print("No files found to merge.")
        return

    # Step 2: List files for the user
    print("\n--- Files found in directory and subdirectories ---")
    for i, file_path in enumerate(all_files):
        print(f"[{i}] {file_path}")

    # Step 3: Caller chooses exclusions
    exclude_input = input("\nEnter the numbers to EXCLUDE (comma-separated, e.g., 0,2,5) or press Enter to include all: ")
    
    exclude_indices = set()
    if exclude_input.strip():
        try:
            exclude_indices = {int(x.strip()) for x in exclude_input.split(',')}
        except ValueError:
            print("Invalid input. Proceeding without exclusions.")

    # Step 4: Process and Merge
    output_filename = "merged_output.txt"
    with open(output_filename, 'w', encoding='utf-8') as outfile:
        for i, file_path in enumerate(all_files):
            if i in exclude_indices:
                print(f"Skipping: {file_path}")
                continue
            
            print(f"Adding: {file_path}")
            
            # Header with Relative Path
            outfile.write(f"FILE PATH: {file_path}\n")
            outfile.write("-" * 60 + "\n")
            
            # Content
            try:
                with open(file_path, 'r', encoding='utf-8', errors='ignore') as infile:
                    outfile.write(infile.read())
            except Exception as e:
                outfile.write(f"[Error reading file: {e}]")
            
            # Footer / Break
            outfile.write("\n\n" + "="*60 + "\n\n")

    print(f"\nSuccess! Everything combined into: {output_filename}")

if __name__ == "__main__":
    merge_files_recursive()