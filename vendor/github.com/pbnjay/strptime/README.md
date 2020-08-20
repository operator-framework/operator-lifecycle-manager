Strptime
========

Simple C-style strptime wrappers for Go's `time.Parse`. Support is available
for the following subset of format strings (descriptions blatantly stolen from
python docs):

    %d  Day of the month as a zero-padded decimal number.
    %b  Month as locale’s abbreviated name.
    %B  Month as locale’s full name.
    %m  Month as a zero-padded decimal number.
    %y  Year without century as a zero-padded decimal number.
    %Y  Year with century as a decimal number.
    %H  Hour (24-hour clock) as a zero-padded decimal number.
    %I  Hour (12-hour clock) as a zero-padded decimal number.
    %p  Locale’s equivalent of either AM or PM.
    %M  Minute as a zero-padded decimal number.
    %S  Second as a zero-padded decimal number.
    %f  Microsecond as a decimal number, zero-padded on the left.
    %z  UTC offset in the form +HHMM or -HHMM.
    %Z  Time zone name. UTC, EST, CST
    %%  A literal '%' character.

A small test suite is included to test many common use cases. Code is available
under the MIT License in case anyone else has a need for it like I do.

