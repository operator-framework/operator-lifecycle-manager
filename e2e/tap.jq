if .Test !=null then 
  . 
else 
  empty 
end 

| 

if .Action == "fail" then 
  "not ok # \(.Test)" 
elif .Action == "pass" then 
  "ok # \(.Test)" 
elif .Action == "skip" then 
  "ok # skip \(.Test)" 
elif .Action == "output" then
  "# \(.Output)" | rtrimstr("\n")
else 
  empty 
end
