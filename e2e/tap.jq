if .Test !=null then 
  . 
else 
  empty 
end 

| 

if .Action == "fail" and .Test then 
  "not ok # \(.Test)" 
elif .Action == "pass" and .Test then 
  "ok # \(.Test)" 
elif .Action == "skip" and .Test then 
  "ok # skip \(.Test)" 
else 
  empty 
end
