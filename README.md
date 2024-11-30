## introduction

It helps to find all maven projects in a parent directory, and then execute 
mvn commands(mvn clean and so on) in the same time. This will generate three
program output log in the same directory in which the program runs.

## examples

go build -o maven-runner
./maven-runner --cmd="mvn clean package" --dir="/path/to/parent/dir" --retry=5 --concurrency=5

如果不提供 --dir 参数，默认会使用当前工作目录作为父目录
./maven-runner --cmd="mvn clean package" --retry=5 --concurrency=5

## real usage
go build -o test

./test --cmd="mvn clean install" --dir="/Users/abs/path/to/some/maven/parent/path"

./test --cmd="mvn clean install" --dir="./relative/path"

./test --cmd="mvn clean install" --dir="."
