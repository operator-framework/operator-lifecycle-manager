# Set the delimiter
IFS=';'

# Read the split words into an array
read -ra skipped_tests <<< "$1"

# Construct the skip arguments for the e2e test
output=""
for test in "${skipped_tests[@]}";
do
 output="$output -skip '$test'"
done

echo $output
